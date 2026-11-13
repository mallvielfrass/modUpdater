package mods

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"modupdater/internal/store"
)

const maxIndex = 8 << 20

type ImportResult struct {
	Added        int      `json:"added"`
	Updated      int      `json:"updated"`
	Skipped      int      `json:"skipped"`
	Failed       []string `json:"failed"`
	AddedFiles   []string `json:"added_files"`
	SkippedFiles []string `json:"skipped_files"`
}

type packFile struct {
	Path      string   `json:"path"`
	Hashes    hashes   `json:"hashes"`
	Env       packEnv  `json:"env"`
	Downloads []string `json:"downloads"`
	FileSize  int64    `json:"fileSize"`
}

type hashes struct {
	SHA1   string `json:"sha1"`
	SHA512 string `json:"sha512"`
}

type packEnv struct {
	Client string `json:"client"`
	Server string `json:"server"`
}

func (s *Service) ImportModrinth(r io.Reader, client *http.Client) (ImportResult, error) {
	res := ImportResult{Failed: []string{}, AddedFiles: []string{}, SkippedFiles: []string{}}
	data, err := io.ReadAll(io.LimitReader(r, maxIndex+1))
	if err != nil {
		return res, err
	}
	if int64(len(data)) > maxIndex {
		return res, ErrPayloadTooLarge
	}
	var doc struct {
		Files []packFile `json:"files"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return res, ErrInvalidJSON
	}
	if client == nil {
		client = http.DefaultClient
	}
	for _, file := range doc.Files {
		name, ok, err := modJarName(file.Path)
		if err != nil {
			res.Failed = append(res.Failed, failLine(file.Path, ErrInvalidFilename))
			continue
		}
		if !ok {
			continue
		}
		clientOnly, clientFlag, skip, err := packSide(file.Env)
		if err != nil {
			res.Failed = append(res.Failed, failLine(file.Path, err))
			continue
		}
		if skip {
			continue
		}
		body, err := fetchPackFile(client, file)
		if err != nil {
			res.Failed = append(res.Failed, failLine(file.Path, err))
			continue
		}
		meta, sum, err := metaFromBytes(body, name)
		if err != nil {
			res.Failed = append(res.Failed, failLine(file.Path, err))
			continue
		}
		if err := s.savePackFile(name, body, meta, sum, clientOnly, clientFlag, &res, file.Path); err != nil {
			return res, err
		}
	}
	return res, nil
}

func (s *Service) savePackFile(name string, body []byte, meta Meta, sum string, clientOnly, clientFlag bool, res *ImportResult, path string) error {
	existing, err := s.db.ModBySlug(meta.Slug)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if err == nil {
		if existing.ClientOnly != clientOnly {
			res.Failed = append(res.Failed, failLine(path, ErrConflict))
			return nil
		}
		if existing.SHA256 == sum && existing.Client == clientFlag {
			res.Skipped++
			res.SkippedFiles = append(res.SkippedFiles, name)
			return nil
		}
		up := Upload{Client: &clientFlag}
		if existing.SHA256 != sum {
			up.Filename = name
			up.File = io.NopCloser(bytes.NewReader(body))
		}
		if _, err := s.Update(existing.ID, up); err != nil {
			res.Failed = append(res.Failed, failLine(path, err))
			return nil
		}
		res.Updated++
		return nil
	}
	only := clientOnly
	flag := clientFlag
	_, err = s.Add(Upload{
		Filename:   name,
		File:       io.NopCloser(bytes.NewReader(body)),
		Client:     &flag,
		ClientOnly: &only,
	})
	if err != nil {
		res.Failed = append(res.Failed, failLine(path, err))
		return nil
	}
	res.Added++
	res.AddedFiles = append(res.AddedFiles, name)
	return nil
}

func failLine(path string, err error) string {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "?"
	}
	code := "download"
	switch {
	case errors.Is(err, ErrInvalidJSON):
		code = "invalid_json"
	case errors.Is(err, ErrInvalidFilename), errors.Is(err, ErrBadPath):
		code = "invalid_filename"
	case errors.Is(err, ErrConflict):
		code = "conflict"
	case errors.Is(err, ErrSlugExists):
		code = "slug_exists"
	case errors.Is(err, ErrModsDirUnset):
		code = "mods_dir_unset"
	case errors.Is(err, ErrPayloadTooLarge):
		code = "payload_too_large"
	case errors.Is(err, errPackHash):
		code = "hash"
	case errors.Is(err, errPackSize):
		code = "size"
	}
	return path + ": " + code
}

var (
	errPackHash = errors.New("hash")
	errPackSize = errors.New("size")
)

func modJarName(path string) (string, bool, error) {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	if path == "" || strings.Contains(path, "..") {
		return "", false, ErrInvalidFilename
	}
	if path != "mods" && !strings.HasPrefix(path, "mods/") {
		return "", false, nil
	}
	name, err := SafeJarName(path)
	if err != nil {
		return "", false, ErrInvalidFilename
	}
	return name, true, nil
}

func packSide(env packEnv) (clientOnly, client bool, skip bool, err error) {
	c, err := normPackEnv(env.Client)
	if err != nil {
		return false, false, false, err
	}
	s, err := normPackEnv(env.Server)
	if err != nil {
		return false, false, false, err
	}
	cOn := c != "unsupported"
	sOn := s != "unsupported"
	switch {
	case cOn && sOn:
		return false, true, false, nil
	case cOn:
		return true, true, false, nil
	case sOn:
		return false, false, false, nil
	default:
		return false, false, true, nil
	}
}

func normPackEnv(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		v = "required"
	}
	switch v {
	case "required", "optional", "unsupported":
		return v, nil
	default:
		return "", ErrInvalidJSON
	}
}

func fetchPackFile(client *http.Client, file packFile) ([]byte, error) {
	if err := checkPackHash(file); err != nil {
		return nil, err
	}
	if file.FileSize > MaxJar {
		return nil, ErrPayloadTooLarge
	}
	if len(file.Downloads) == 0 {
		return nil, errors.New("download")
	}
	var last error
	for _, raw := range file.Downloads {
		body, err := fetchOne(client, raw, file)
		if err == nil {
			return body, nil
		}
		last = err
	}
	if last == nil {
		return nil, errPackHash
	}
	return nil, last
}

func checkPackHash(file packFile) error {
	sha1hex := strings.TrimSpace(file.Hashes.SHA1)
	sha512hex := strings.TrimSpace(file.Hashes.SHA512)
	if sha1hex == "" && sha512hex == "" {
		return errPackHash
	}
	if sha1hex != "" && !validHex(sha1hex, sha1.Size) {
		return errPackHash
	}
	if sha512hex != "" && !validHex(sha512hex, sha512.Size) {
		return errPackHash
	}
	return nil
}

func validHex(s string, size int) bool {
	if len(s) != size*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func fetchOne(client *http.Client, raw string, file packFile) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, errors.New("download")
	}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("download")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("download")
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, errors.New("download")
	}
	limit := MaxJar
	if file.FileSize > 0 && file.FileSize < limit {
		limit = file.FileSize
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errPackHash
	}
	if int64(len(body)) > MaxJar || (file.FileSize > 0 && int64(len(body)) != file.FileSize) {
		return nil, errPackSize
	}
	if file.Hashes.SHA1 != "" || file.Hashes.SHA512 != "" {
		got1, got512 := hashBytes(body, file.Hashes.SHA1 != "", file.Hashes.SHA512 != "")
		if file.Hashes.SHA1 != "" && !strings.EqualFold(got1, file.Hashes.SHA1) {
			return nil, errPackHash
		}
		if file.Hashes.SHA512 != "" && !strings.EqualFold(got512, file.Hashes.SHA512) {
			return nil, errPackHash
		}
	}
	return body, nil
}

func hashBytes(data []byte, want1, want512 bool) (string, string) {
	var h1, h512 hash.Hash
	if want1 {
		h1 = sha1.New()
	}
	if want512 {
		h512 = sha512.New()
	}
	if h1 != nil {
		_, _ = h1.Write(data)
	}
	if h512 != nil {
		_, _ = h512.Write(data)
	}
	var s1, s512 string
	if h1 != nil {
		s1 = hex.EncodeToString(h1.Sum(nil))
	}
	if h512 != nil {
		s512 = hex.EncodeToString(h512.Sum(nil))
	}
	return s1, s512
}

func metaFromBytes(data []byte, name string) (Meta, string, error) {
	f, err := os.CreateTemp("", "modrinth-*.part")
	if err != nil {
		return Meta{}, "", err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return Meta{}, "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return Meta{}, "", err
	}
	meta, err := ReadMeta(f, name)
	f.Close()
	if err != nil {
		return Meta{}, "", err
	}
	sum := sha256.Sum256(data)
	return meta, hex.EncodeToString(sum[:]), nil
}
