package chunkbackup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var ErrManifest = errors.New("manifest")

type FileEntry struct {
	Path   string   `json:"path"`
	Mode   uint32   `json:"mode"`
	Size   int64    `json:"size,omitempty"`
	Link   string   `json:"link,omitempty"`
	Chunks []string `json:"chunks,omitempty"`
}

type Manifest struct {
	Version int         `json:"version"`
	ID      string      `json:"id"`
	Kind    string      `json:"kind"`
	Scope   string      `json:"scope"`
	Parent  string      `json:"parent,omitempty"`
	Roots   []string    `json:"roots,omitempty"`
	Files   []FileEntry `json:"files"`
	Deleted []string    `json:"deleted,omitempty"`
}

func ManifestPath(root, id string) string {
	return filepath.Join(root, "manifests", id+".json")
}

func marshalManifest(m Manifest) ([]byte, error) {
	if m.Files == nil {
		m.Files = []FileEntry{}
	}
	return json.Marshal(m)
}

func SaveManifest(path string, m Manifest) error {
	raw, err := marshalManifest(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func LoadManifest(path string) (Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, ErrManifest
	}
	return m, nil
}

type canonFile struct {
	Path   string   `json:"path"`
	Mode   uint32   `json:"mode"`
	Link   string   `json:"link,omitempty"`
	Chunks []string `json:"chunks,omitempty"`
}

type canonManifest struct {
	Files   []canonFile `json:"files"`
	Deleted []string    `json:"deleted,omitempty"`
}

func ContentHash(m Manifest) (string, error) {
	files := append([]FileEntry(nil), m.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	deleted := append([]string(nil), m.Deleted...)
	sort.Strings(deleted)
	c := canonManifest{Files: make([]canonFile, 0, len(files))}
	for _, f := range files {
		item := canonFile{Path: f.Path, Mode: f.Mode, Link: f.Link, Chunks: append([]string(nil), f.Chunks...)}
		if len(item.Chunks) == 0 {
			item.Chunks = nil
		}
		c.Files = append(c.Files, item)
	}
	if len(deleted) > 0 {
		c.Deleted = deleted
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(c); err != nil {
		return "", err
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:]), nil
}

func HashSet(files []FileEntry) map[string]struct{} {
	out := map[string]struct{}{}
	for _, f := range files {
		for _, hash := range f.Chunks {
			out[hash] = struct{}{}
		}
	}
	return out
}

func DeletedPaths(parent, current []FileEntry) []string {
	cur := map[string]struct{}{}
	for _, f := range current {
		cur[f.Path] = struct{}{}
	}
	var out []string
	for _, f := range parent {
		if _, ok := cur[f.Path]; !ok {
			out = append(out, f.Path)
		}
	}
	sort.Strings(out)
	return out
}

func Apply(parent, snap Manifest) Manifest {
	by := map[string]FileEntry{}
	var order []string
	for _, f := range parent.Files {
		if _, ok := by[f.Path]; !ok {
			order = append(order, f.Path)
		}
		by[f.Path] = f
	}
	gone := map[string]struct{}{}
	for _, d := range snap.Deleted {
		gone[d] = struct{}{}
		delete(by, d)
	}
	for _, f := range snap.Files {
		if _, ok := by[f.Path]; !ok {
			order = append(order, f.Path)
		}
		by[f.Path] = f
		delete(gone, f.Path)
	}
	out := snap
	out.Files = nil
	out.Deleted = nil
	seen := map[string]struct{}{}
	for _, p := range order {
		if _, ok := gone[p]; ok {
			continue
		}
		f, ok := by[p]
		if !ok {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out.Files = append(out.Files, f)
	}
	if len(snap.Roots) > 0 {
		out.Roots = append([]string(nil), snap.Roots...)
	}
	return out
}

func OwnHashes(files []FileEntry, parent []FileEntry) []string {
	skip := HashSet(parent)
	seen := map[string]struct{}{}
	var out []string
	for _, f := range files {
		for _, hash := range f.Chunks {
			if _, ok := skip[hash]; ok {
				continue
			}
			if _, ok := seen[hash]; ok {
				continue
			}
			seen[hash] = struct{}{}
			out = append(out, hash)
		}
	}
	return out
}

func PrefixStats(files []FileEntry, prefix string) (int64, int64) {
	pre := prefix + "/"
	var n, b int64
	for _, f := range files {
		if !strings.HasPrefix(f.Path, pre) {
			continue
		}
		if os.FileMode(f.Mode)&os.ModeSymlink != 0 {
			continue
		}
		n++
		b += f.Size
	}
	return n, b
}

func Ingest(st *Store, src, prefix string, onBytes func(int64), onFile func()) ([]FileEntry, int64, error) {
	var entries []FileEntry
	var fresh int64
	src = filepath.Clean(src)
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ErrManifest
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
		name := prefix + "/" + filepath.ToSlash(rel)
		if !validRel(name) {
			return ErrManifest
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				if errors.Is(err, os.ErrPermission) {
					return nil
				}
				return err
			}
			entries = append(entries, FileEntry{Path: name, Mode: uint32(info.Mode()), Link: link})
			return nil
		}
		if d.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		hashes, n, size, err := st.PutFile(path, onBytes)
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
		fresh += n
		entries = append(entries, FileEntry{Path: name, Mode: uint32(info.Mode()), Size: size, Chunks: hashes})
		if onFile != nil {
			onFile()
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, fresh, nil
}

func MeasureDir(root string) (int64, string, error) {
	root = filepath.Clean(root)
	var paths []string
	var size int64
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ErrManifest
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if info.Mode()&os.ModeSymlink == 0 && !info.Mode().IsRegular() {
			return nil
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			sum := sha256.Sum256(nil)
			return 0, hex.EncodeToString(sum[:]), nil
		}
		return 0, "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, rel := range paths {
		full := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(full)
		if err != nil {
			return 0, "", err
		}
		if _, err := fmt.Fprintf(h, "%s\n%d\n", rel, uint32(info.Mode())); err != nil {
			return 0, "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(full)
			if err != nil {
				return 0, "", err
			}
			if _, err := fmt.Fprintf(h, "%s\n", link); err != nil {
				return 0, "", err
			}
			continue
		}
		f, err := os.Open(full)
		if err != nil {
			return 0, "", err
		}
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return 0, "", copyErr
		}
		if closeErr != nil {
			return 0, "", closeErr
		}
		if _, err := h.Write([]byte{0}); err != nil {
			return 0, "", err
		}
	}
	return size, hex.EncodeToString(h.Sum(nil)), nil
}

func validRel(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func validTop(p string) bool {
	switch p {
	case "mods", "config", "world":
		return true
	default:
		return false
	}
}

func inside(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}
