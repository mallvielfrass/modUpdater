package mods

import (
	"archive/tar"
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func writeBackupTar(tw *tar.Writer, dir string) error {
	for _, name := range []string{"mods", "config", "world"} {
		src := filepath.Join(dir, name)
		st, err := os.Lstat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return ErrBackupCopy
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			continue
		}
		if err := addBackupTar(tw, src, name); err != nil {
			return err
		}
	}
	return nil
}

func addBackupTar(tw *tar.Writer, src, prefix string) error {
	src = filepath.Clean(src)
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return ErrBackupCopy
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ErrBackupCopy
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return ErrBackupCopy
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() {
			return nil
		}
		name := prefix
		if rel != "." {
			name = prefix + "/" + strings.ReplaceAll(rel, string(filepath.Separator), "/")
		}
		var file *os.File
		if info.IsDir() {
			if !strings.HasSuffix(name, "/") {
				name += "/"
			}
		} else {
			opened, openErr := os.Open(path)
			if openErr != nil {
				if errors.Is(openErr, os.ErrPermission) {
					return nil
				}
				return ErrBackupCopy
			}
			defer opened.Close()
			file = opened
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return ErrBackupCopy
		}
		hdr.Name = name
		hdr.Format = tar.FormatUSTAR
		if err := tw.WriteHeader(hdr); err != nil {
			hdr.Format = 0
			if err = tw.WriteHeader(hdr); err != nil {
				return ErrBackupCopy
			}
		}
		if file == nil {
			return nil
		}
		if _, err := io.Copy(tw, file); err != nil {
			return ErrBackupCopy
		}
		return nil
	})
}

func extractWorldZip(zipPath, worldDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return ErrBadWorldZip
	}
	defer zr.Close()
	worldDir = filepath.Clean(worldDir)
	found := false
	for _, f := range zr.File {
		rel, ok := safeZipRel(f.Name)
		if !ok {
			return ErrBadWorldZip
		}
		if rel == "" {
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return ErrBadWorldZip
		}
		target := filepath.Join(worldDir, filepath.FromSlash(rel))
		target = filepath.Clean(target)
		if !pathInside(worldDir, target) {
			return ErrBadWorldZip
		}
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return ErrBackupCopy
			}
			continue
		}
		if rel == "level.dat" {
			found = true
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return ErrBackupCopy
		}
		rc, err := f.Open()
		if err != nil {
			return ErrBadWorldZip
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			rc.Close()
			return ErrBackupCopy
		}
		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		rc.Close()
		if copyErr != nil {
			return ErrBackupCopy
		}
		if closeErr != nil {
			return ErrBackupCopy
		}
	}
	if !found {
		return ErrNoWorldLevel
	}
	return nil
}

func safeZipRel(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.ContainsRune(name, 0) {
		return "", false
	}
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return "", true
	}
	var parts []string
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", false
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "/"), true
}

func copyTree(src, dst string, note *treeNote) error {
	src = filepath.Clean(src)
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if isBusy(err) {
				return ErrBackupBusy
			}
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ErrBackupCopy
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			if isBusy(err) {
				return ErrBackupBusy
			}
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if err := copyLink(path, target); err != nil {
				if errors.Is(err, os.ErrPermission) {
					return nil
				}
				return err
			}
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		var onBytes func(int64)
		if note != nil {
			onBytes = note.bytes
		}
		if err := copyFile(path, target, 0o644, onBytes); err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
		if note != nil && note.file != nil {
			note.file()
		}
		return nil
	})
}

func copyLink(src, dst string) error {
	link, err := os.Readlink(src)
	if err != nil {
		if isBusy(err) {
			return ErrBackupBusy
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Symlink(link, dst)
}

func copyFile(src, dst string, perm os.FileMode, onBytes func(int64)) error {
	in, err := os.Open(src)
	if err != nil {
		if isBusy(err) {
			return ErrBackupBusy
		}
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if perm == 0 {
		perm = 0o644
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	var reader io.Reader = in
	if onBytes != nil {
		reader = &byteTap{r: in, emit: onBytes}
	}
	_, copyErr := io.Copy(out, reader)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(dst, perm)
}

type byteTap struct {
	r    io.Reader
	emit func(int64)
	pend int64
}

func (b *byteTap) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if n > 0 {
		b.pend += int64(n)
	}
	if b.pend > 0 && (b.pend >= 1<<20 || err != nil) {
		b.emit(b.pend)
		b.pend = 0
	}
	return n, err
}
