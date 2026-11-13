package mods

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	jarRe     = regexp.MustCompile(`^[A-Za-z0-9(\[][A-Za-z0-9._+()\[\] -]*\.jar$`)
	archiveRe = regexp.MustCompile(`^[0-9a-f]{32}\.jar$`)

	ErrBadPath = errors.New("bad path")
)

func SafeJarName(raw string) (string, error) {
	if raw == "" || strings.ContainsAny(raw, "\x00\r\n\"") {
		return "", ErrBadPath
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == '\\' })
	for _, p := range parts {
		if p == ".." {
			return "", ErrBadPath
		}
	}
	if len(parts) == 0 {
		return "", ErrBadPath
	}
	name := parts[len(parts)-1]
	if len(name) < 5 || len(name) > 180 || strings.Contains(name, "..") || !jarRe.MatchString(name) {
		return "", ErrBadPath
	}
	return name, nil
}

func SafeArchiveName(name string) bool {
	return archiveRe.MatchString(name)
}

func ResolveModsDir(dataDir, raw string) (string, error) {
	if raw == "" {
		return "", ErrBadPath
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", ErrBadPath
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", ErrBadPath
	}
	real = filepath.Clean(real)
	st, err := os.Stat(real)
	if err != nil || !st.IsDir() {
		return "", ErrBadPath
	}
	if real == string(filepath.Separator) {
		return "", ErrBadPath
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", err
	}
	dataAbs, err := filepath.Abs(dataDir)
	if err != nil {
		return "", ErrBadPath
	}
	dataReal, err := filepath.EvalSymlinks(dataAbs)
	if err != nil {
		return "", ErrBadPath
	}
	dataReal = filepath.Clean(dataReal)
	if dataReal == real || pathInside(dataReal, real) || pathInside(real, dataReal) {
		return "", ErrBadPath
	}
	return real, nil
}

func pathInside(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil || rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func OpenPlain(root, name string) (*os.File, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") || filepath.IsAbs(name) {
		return nil, ErrBadPath
	}
	root = filepath.Clean(root)
	path := filepath.Join(root, name)
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return nil, ErrBadPath
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel != name {
		return nil, ErrBadPath
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, ErrBadPath
	}
	return f, nil
}

func OpenRegular(root, name string) (*os.File, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") || filepath.IsAbs(name) {
		return nil, ErrBadPath
	}
	cleaned, err := SafeJarName(name)
	if err != nil || cleaned != name {
		return nil, ErrBadPath
	}
	root = filepath.Clean(root)
	path := filepath.Join(root, name)
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return nil, ErrBadPath
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel != name {
		return nil, ErrBadPath
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, ErrBadPath
	}
	return f, nil
}
