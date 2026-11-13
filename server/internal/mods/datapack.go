package mods

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"modupdater/internal/store"
)

const (
	MaxDatapack      int64 = 256 << 20
	maxDatapackRaw   int64 = 512 << 20
	maxDatapackFiles       = 65536
)

var (
	ErrNotDatapack      = errors.New("В архиве нет pack.mcmeta.")
	ErrBadDatapack      = errors.New("Архив датапака повреждён.")
	ErrBadDatapackName  = errors.New("Некорректное имя датапака.")
	ErrDatapackWrite    = errors.New("Не удалось записать датапак.")
	ErrServerProperties = errors.New("Не удалось прочитать server.properties.")
	ErrDatapackPath     = errors.New("Некорректный путь файла.")
	ErrDatapackText     = errors.New("Файл не является текстом.")
	ErrDatapackFile     = errors.New("Файл не найден.")
	ErrDatapackFolder   = errors.New("Датапак не найден.")
	ErrDatapackBig      = errors.New("Слишком большой файл.")
)

type DatapackAccessError struct {
	Dir string
}

func (e *DatapackAccessError) Error() string {
	return "Нет доступа к каталогу датапаков " + e.Dir + "."
}

type Datapack struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (s *Service) ListDatapacks() ([]Datapack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	world, err := s.datapackWorld()
	if err != nil {
		return nil, err
	}
	packs, err := s.datapackDir(world)
	if err != nil {
		if os.IsNotExist(err) {
			return []Datapack{}, nil
		}
		return nil, asDatapackAccess(filepath.Join(world, "datapacks"), err)
	}
	entries, err := os.ReadDir(packs)
	if err != nil {
		if os.IsNotExist(err) {
			return []Datapack{}, nil
		}
		return nil, asDatapackAccess(packs, err)
	}
	out := []Datapack{}
	for _, ent := range entries {
		name := ent.Name()
		if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") {
			continue
		}
		info, err := ent.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		full := filepath.Join(packs, name)
		if info.IsDir() {
			out = append(out, Datapack{Name: name, Description: fileDescription(filepath.Join(full, "pack.mcmeta"))})
			continue
		}
		if info.Mode().IsRegular() && strings.HasSuffix(strings.ToLower(name), ".zip") {
			out = append(out, Datapack{Name: name, Description: zipDescription(full)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (s *Service) InstallDatapack(filename string, r io.Reader) (Datapack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name, err := datapackName(filename)
	if err != nil {
		return Datapack{}, err
	}
	tmp, err := s.spoolDatapack(r)
	if err != nil {
		return Datapack{}, err
	}
	defer os.Remove(tmp)
	prefix, err := inspectDatapack(tmp)
	if err != nil {
		return Datapack{}, err
	}
	world, err := s.datapackWorld()
	if err != nil {
		return Datapack{}, err
	}
	packs, err := ensureDatapacks(world)
	if err != nil {
		return Datapack{}, err
	}
	id, err := store.NewID()
	if err != nil {
		return Datapack{}, ErrDatapackWrite
	}
	stage := filepath.Join(packs, ".stage-"+id)
	if !pathInside(packs, stage) {
		return Datapack{}, ErrDatapackWrite
	}
	if err := os.Mkdir(stage, 0o755); err != nil {
		return Datapack{}, asDatapackAccess(packs, err)
	}
	stageLeft := true
	defer func() {
		if stageLeft {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := extractDatapack(tmp, stage, prefix); err != nil {
		return Datapack{}, err
	}
	if err := os.Chmod(stage, 0o755); err != nil {
		return Datapack{}, mapDatapackIO(err)
	}
	if err := swapDatapack(packs, name, stage); err != nil {
		return Datapack{}, err
	}
	stageLeft = false
	return Datapack{Name: name, Description: fileDescription(filepath.Join(packs, name, "pack.mcmeta"))}, nil
}

func (s *Service) datapackWorld() (string, error) {
	_, _, world, err := s.layout()
	if errors.Is(err, ErrBackupDenied) {
		return "", ErrServerProperties
	}
	if err != nil {
		return "", err
	}
	world = filepath.Clean(world)
	if real, err := filepath.EvalSymlinks(world); err == nil {
		world = filepath.Clean(real)
	} else if !os.IsNotExist(err) && !errors.Is(err, os.ErrPermission) {
		return "", ErrDatapackWrite
	}
	if err := s.rejectStore(world); err != nil {
		return "", err
	}
	return world, nil
}

func (s *Service) datapackDir(world string) (string, error) {
	packs := filepath.Join(world, "datapacks")
	real, err := filepath.EvalSymlinks(packs)
	if err != nil {
		return "", err
	}
	real = filepath.Clean(real)
	if !pathInside(world, real) {
		return "", ErrDatapackWrite
	}
	return real, nil
}

func (s *Service) spoolDatapack(r io.Reader) (string, error) {
	if err := os.MkdirAll(s.dataDir, 0o755); err != nil {
		return "", ErrDatapackWrite
	}
	f, err := os.CreateTemp(s.dataDir, "datapack-*.zip")
	if err != nil {
		return "", ErrDatapackWrite
	}
	name := f.Name()
	n, copyErr := io.Copy(f, io.LimitReader(r, MaxDatapack+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(name)
		return "", ErrDatapackWrite
	}
	if n > MaxDatapack {
		os.Remove(name)
		return "", ErrPayloadTooLarge
	}
	return name, nil
}

func ensureDatapacks(world string) (string, error) {
	st, err := os.Stat(world)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return "", &DatapackAccessError{Dir: filepath.Join(world, "datapacks")}
		}
		return "", ErrDatapackWrite
	}
	if !st.IsDir() {
		return "", ErrDatapackWrite
	}
	packs := filepath.Join(world, "datapacks")
	if _, err := os.Lstat(packs); err != nil {
		if !os.IsNotExist(err) {
			return "", asDatapackAccess(packs, err)
		}
		if err := os.Mkdir(packs, 0o755); err != nil {
			return "", asDatapackAccess(packs, err)
		}
	}
	real, err := filepath.EvalSymlinks(packs)
	if err != nil {
		return "", asDatapackAccess(packs, err)
	}
	real = filepath.Clean(real)
	if !pathInside(world, real) {
		return "", ErrDatapackWrite
	}
	if err := os.Chmod(real, 0o755); err != nil && !errors.Is(err, os.ErrPermission) {
		return "", mapDatapackIO(err)
	}
	return real, nil
}

func asDatapackAccess(dir string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrPermission) {
		return &DatapackAccessError{Dir: filepath.Clean(dir)}
	}
	return mapDatapackIO(err)
}

func datapackName(filename string) (string, error) {
	filename = strings.ReplaceAll(filename, "\\", "/")
	if strings.ContainsRune(filename, 0) {
		return "", ErrBadDatapackName
	}
	filename = path.Base(filename)
	filename = strings.TrimSpace(filename)
	if strings.HasSuffix(strings.ToLower(filename), ".zip") {
		filename = strings.TrimSpace(filename[:len(filename)-len(".zip")])
	}
	if filename == "" || filename == "." || filename == ".." || len(filename) > 180 {
		return "", ErrBadDatapackName
	}
	if strings.HasPrefix(filename, ".") || strings.ContainsAny(filename, "/\\") || filename != filepath.Clean(filename) {
		return "", ErrBadDatapackName
	}
	for _, r := range filename {
		if r < 0x20 || r == 0x7f {
			return "", ErrBadDatapackName
		}
	}
	return filename, nil
}

func inspectDatapack(zipPath string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", ErrNotDatapack
	}
	defer zr.Close()
	return datapackPrefix(zr.File)
}

func datapackPrefix(files []*zip.File) (string, error) {
	tops := map[string]struct{}{}
	rootMeta := false
	folder := ""
	folderMeta := 0
	for _, f := range files {
		if f.Mode()&os.ModeSymlink != 0 {
			return "", ErrBadDatapack
		}
		rel, ok := safeZipRel(f.Name)
		if !ok {
			return "", ErrBadDatapack
		}
		if rel == "" {
			continue
		}
		parts := strings.Split(rel, "/")
		tops[parts[0]] = struct{}{}
		if zipEntryDir(f) {
			continue
		}
		if rel == "pack.mcmeta" {
			rootMeta = true
			continue
		}
		if len(parts) == 2 && parts[1] == "pack.mcmeta" {
			folder = parts[0]
			folderMeta++
		}
	}
	if rootMeta {
		return "", nil
	}
	if folderMeta == 1 && len(tops) == 1 && folder != "" {
		return folder, nil
	}
	return "", ErrNotDatapack
}

func extractDatapack(zipPath, dest, prefix string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return ErrNotDatapack
	}
	defer zr.Close()
	var written int64
	files := 0
	for _, f := range zr.File {
		if f.Mode()&os.ModeSymlink != 0 {
			return ErrBadDatapack
		}
		rel, ok := safeZipRel(f.Name)
		if !ok {
			return ErrBadDatapack
		}
		if rel == "" {
			continue
		}
		inner := rel
		if prefix != "" {
			if rel == prefix {
				continue
			}
			pfx := prefix + "/"
			if !strings.HasPrefix(rel, pfx) {
				return ErrBadDatapack
			}
			inner = rel[len(pfx):]
			if inner == "" {
				continue
			}
		}
		target := filepath.Clean(filepath.Join(dest, filepath.FromSlash(inner)))
		if !pathInside(dest, target) {
			return ErrBadDatapack
		}
		if zipEntryDir(f) {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return mapDatapackIO(err)
			}
			continue
		}
		files++
		if files > maxDatapackFiles {
			return ErrDatapackWrite
		}
		n, err := writeZipFile(f, target, maxDatapackRaw-written)
		if err != nil {
			return err
		}
		written += n
	}
	st, err := os.Lstat(filepath.Join(dest, "pack.mcmeta"))
	if err != nil || !st.Mode().IsRegular() {
		return ErrNotDatapack
	}
	return nil
}

func writeZipFile(f *zip.File, target string, remain int64) (int64, error) {
	if remain < 0 {
		return 0, ErrPayloadTooLarge
	}
	rc, err := f.Open()
	if err != nil {
		return 0, ErrBadDatapack
	}
	defer rc.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, mapDatapackIO(err)
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, mapDatapackIO(err)
	}
	n, copyErr := io.Copy(out, io.LimitReader(rc, remain+1))
	closeErr := out.Close()
	if n > remain {
		return n, ErrPayloadTooLarge
	}
	if copyErr != nil {
		return n, mapDatapackIO(copyErr)
	}
	if closeErr != nil {
		return n, mapDatapackIO(closeErr)
	}
	return n, nil
}

func swapDatapack(packs, name, stage string) error {
	dest := filepath.Join(packs, name)
	if !pathInside(packs, dest) {
		return ErrBadDatapackName
	}
	bak := filepath.Join(packs, ".bak-"+strings.TrimPrefix(filepath.Base(stage), ".stage-"))
	if !pathInside(packs, bak) {
		return ErrDatapackWrite
	}
	if _, err := os.Lstat(bak); err == nil {
		if err := os.RemoveAll(bak); err != nil {
			return mapDatapackIO(err)
		}
	} else if !os.IsNotExist(err) {
		return mapDatapackIO(err)
	}
	if _, err := os.Lstat(dest); err == nil {
		if err := os.Rename(dest, bak); err != nil {
			return mapDatapackIO(err)
		}
		if err := os.Rename(stage, dest); err != nil {
			_ = os.Rename(bak, dest)
			return mapDatapackIO(err)
		}
		_ = os.RemoveAll(bak)
		return nil
	} else if !os.IsNotExist(err) {
		return mapDatapackIO(err)
	}
	if err := os.Rename(stage, dest); err != nil {
		return mapDatapackIO(err)
	}
	return nil
}

func zipEntryDir(f *zip.File) bool {
	return f.FileInfo().IsDir() || strings.HasSuffix(strings.ReplaceAll(f.Name, "\\", "/"), "/")
}

func fileDescription(path string) string {
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return ""
	}
	return descriptionFromMeta(data)
}

func zipDescription(zipPath string) string {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return ""
	}
	defer zr.Close()
	prefix, err := datapackPrefix(zr.File)
	if err != nil {
		return ""
	}
	want := "pack.mcmeta"
	if prefix != "" {
		want = prefix + "/pack.mcmeta"
	}
	for _, f := range zr.File {
		if zipEntryDir(f) || f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		rel, ok := safeZipRel(f.Name)
		if !ok || rel != want {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return ""
		}
		data, err := io.ReadAll(io.LimitReader(rc, 1<<20))
		rc.Close()
		if err != nil {
			return ""
		}
		return descriptionFromMeta(data)
	}
	return ""
}

func descriptionFromMeta(data []byte) string {
	data = bytes.TrimPrefix(bytes.TrimSpace(data), []byte{0xEF, 0xBB, 0xBF})
	var root struct {
		Pack struct {
			Description json.RawMessage `json:"description"`
		} `json:"pack"`
	}
	if json.Unmarshal(data, &root) != nil {
		return ""
	}
	return trimText(componentText(root.Pack.Description, 0), 400)
}

func componentText(raw json.RawMessage, depth int) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || depth > 8 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return ""
		}
		return s
	case '[':
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) != nil {
			return ""
		}
		var b strings.Builder
		for _, item := range arr {
			b.WriteString(componentText(item, depth+1))
		}
		return b.String()
	case '{':
		var obj struct {
			Text  string          `json:"text"`
			Extra json.RawMessage `json:"extra"`
		}
		if json.Unmarshal(raw, &obj) != nil {
			return ""
		}
		return obj.Text + componentText(obj.Extra, depth+1)
	default:
		return ""
	}
}

func trimText(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

const maxDatapackText int64 = 1 << 20

func (s *Service) ListDatapackFiles(name string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.datapackFolder(name)
	if err != nil {
		return nil, err
	}
	out := []string{}
	err = filepath.WalkDir(dir, func(filePath string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrPermission) {
				return nil
			}
			return walkErr
		}
		base := d.Name()
		if base == "" || base == "." || base == ".." || strings.HasPrefix(base, ".") {
			if d.IsDir() && base != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, filePath)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") || !pathInside(dir, filePath) {
			return ErrDatapackPath
		}
		rel = filepath.ToSlash(rel)
		if !datapackTextExt(rel) {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, mapDatapackIO(err)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Service) ReadDatapackFile(name, rel string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.datapackFolder(name)
	if err != nil {
		return "", err
	}
	full, err := resolveDatapackFile(dir, rel)
	if err != nil {
		return "", err
	}
	data, err := readPackText(full)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *Service) SaveDatapackFile(name, rel, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if int64(len(text)) > maxDatapackText || !validPackText([]byte(text)) {
		if int64(len(text)) > maxDatapackText {
			return ErrDatapackBig
		}
		return ErrDatapackText
	}
	dir, err := s.datapackFolder(name)
	if err != nil {
		return err
	}
	full, err := resolveDatapackFile(dir, rel)
	if err != nil {
		return err
	}
	if _, err := readPackText(full); err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return asDatapackAccess(dir, err)
	}
	_, writeErr := f.Write([]byte(text))
	closeErr := f.Close()
	if writeErr != nil {
		return asDatapackAccess(dir, writeErr)
	}
	if closeErr != nil {
		return mapDatapackIO(closeErr)
	}
	return nil
}

func (s *Service) datapackFolder(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") || strings.ContainsRune(name, 0) || strings.Contains(name, "..") || strings.HasPrefix(name, ".") || name != filepath.Clean(name) {
		return "", ErrDatapackFolder
	}
	world, err := s.datapackWorld()
	if err != nil {
		return "", err
	}
	packs := filepath.Join(world, "datapacks")
	dir := filepath.Join(packs, name)
	if !pathInside(packs, dir) {
		return "", ErrDatapackPath
	}
	st, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrDatapackFolder
		}
		return "", asDatapackAccess(packs, err)
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return "", ErrDatapackFolder
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", asDatapackAccess(packs, err)
	}
	real = filepath.Clean(real)
	root := packs
	if resolved, err := filepath.EvalSymlinks(packs); err == nil {
		root = filepath.Clean(resolved)
	}
	if !pathInside(root, real) {
		return "", ErrDatapackPath
	}
	return real, nil
}

func resolveDatapackFile(root, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsRune(raw, 0) {
		return "", ErrDatapackPath
	}
	slash := strings.ReplaceAll(raw, "\\", "/")
	if strings.HasPrefix(slash, "/") || filepath.IsAbs(raw) {
		return "", ErrDatapackPath
	}
	parts := strings.Split(slash, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", ErrDatapackPath
		}
	}
	clean := filepath.Clean(filepath.FromSlash(slash))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrDatapackPath
	}
	if !datapackTextExt(clean) {
		return "", ErrDatapackPath
	}
	full := filepath.Join(root, clean)
	if !pathInside(root, full) {
		return "", ErrDatapackPath
	}
	st, err := os.Lstat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrDatapackFile
		}
		return "", asDatapackAccess(root, err)
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
		return "", ErrDatapackPath
	}
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", asDatapackAccess(root, err)
	}
	real = filepath.Clean(real)
	if !pathInside(root, real) || !datapackTextExt(real) {
		return "", ErrDatapackPath
	}
	return real, nil
}

func readPackText(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, asDatapackAccess(filepath.Dir(path), err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, mapDatapackIO(err)
	}
	if st.Size() > maxDatapackText {
		return nil, ErrDatapackBig
	}
	data, err := io.ReadAll(io.LimitReader(f, maxDatapackText+1))
	if err != nil {
		return nil, mapDatapackIO(err)
	}
	if int64(len(data)) > maxDatapackText {
		return nil, ErrDatapackBig
	}
	if !validPackText(data) {
		return nil, ErrDatapackText
	}
	return data, nil
}

func datapackTextExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".json", ".mcmeta", ".mcfunction", ".properties", ".txt", ".snbt":
		return true
	default:
		return false
	}
}

func validPackText(data []byte) bool {
	return !bytes.Contains(data, []byte{0}) && utf8.Valid(data)
}

func mapDatapackIO(err error) error {
	if err == nil || errors.Is(err, ErrNotDatapack) || errors.Is(err, ErrBadDatapack) || errors.Is(err, ErrBadDatapackName) || errors.Is(err, ErrDatapackWrite) || errors.Is(err, ErrPayloadTooLarge) || errors.Is(err, ErrModsDirUnset) || errors.Is(err, ErrInvalidModsDir) || errors.Is(err, ErrServerProperties) || errors.Is(err, ErrDatapackPath) || errors.Is(err, ErrDatapackText) || errors.Is(err, ErrDatapackFile) || errors.Is(err, ErrDatapackFolder) || errors.Is(err, ErrDatapackBig) || errors.Is(err, ErrBackupCopy) || errors.Is(err, ErrBackupBusy) {
		return err
	}
	var access *DatapackAccessError
	if errors.As(err, &access) {
		return err
	}
	if isBusy(err) {
		return ErrBackupBusy
	}
	return ErrDatapackWrite
}
