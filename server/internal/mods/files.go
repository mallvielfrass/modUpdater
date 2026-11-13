package mods

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func presentRegular(dir, name string) bool {
	safe, err := SafeJarName(name)
	if err != nil || safe != name {
		return false
	}
	f, err := OpenRegular(dir, safe)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func readLiveJar(dir, name string) (string, int64, Meta, error) {
	f, err := OpenRegular(dir, name)
	if err != nil {
		return "", 0, Meta{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, Meta{}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", 0, Meta{}, err
	}
	meta, err := ReadMeta(f, name)
	if err != nil {
		return "", 0, Meta{}, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, meta, nil
}

func hashRegular(dir, name string) (string, error) {
	safe, err := SafeJarName(name)
	if err != nil || safe != name {
		return "", ErrBadPath
	}
	f, err := OpenRegular(dir, safe)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeTemp(dir string, r io.Reader, limit int64) (string, string, int64, error) {
	f, err := os.CreateTemp(dir, ".part-*")
	if err != nil {
		return "", "", 0, err
	}
	tmp := f.Name()
	if strings.HasSuffix(tmp, ".jar") {
		f.Close()
		os.Remove(tmp)
		return "", "", 0, errors.New("temp suffix")
	}
	h := sha256.New()
	n, copyErr := io.Copy(f, io.TeeReader(io.LimitReader(r, limit+1), h))
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return "", "", 0, copyErr
	}
	if closeErr != nil {
		os.Remove(tmp)
		return "", "", 0, closeErr
	}
	if n > limit {
		os.Remove(tmp)
		return "", "", 0, ErrPayloadTooLarge
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return "", "", 0, err
	}
	return tmp, hex.EncodeToString(h.Sum(nil)), n, nil
}

func commitTemp(tmp, dir, name string) error {
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func moveJar(srcDir, srcName, dstDir, dstName string) error {
	srcSafe, err := SafeJarName(srcName)
	if err != nil || srcSafe != srcName {
		return ErrBadPath
	}
	dstSafe, err := SafeJarName(dstName)
	if err != nil || dstSafe != dstName {
		return ErrBadPath
	}
	if presentRegular(dstDir, dstName) {
		return ErrConflict
	}
	src := filepath.Join(srcDir, srcName)
	dst := filepath.Join(dstDir, dstName)
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyRegular(srcDir, srcName, dstDir, dstName); err != nil {
		return err
	}
	return os.Remove(src)
}

func relocate(srcDir, srcName, dstDir, dstName string) error {
	if !SafeArchiveName(dstName) {
		return ErrBadPath
	}
	safe, err := SafeJarName(srcName)
	if err != nil || safe != srcName {
		return ErrBadPath
	}
	src := filepath.Join(srcDir, srcName)
	dst := filepath.Join(dstDir, dstName)
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyRegular(srcDir, srcName, dstDir, dstName); err != nil {
		return err
	}
	return os.Remove(src)
}

func copyRegular(srcDir, srcName, dstDir, dstName string) error {
	in, err := OpenRegular(srcDir, srcName)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dstDir, ".part-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, err = io.Copy(tmp, in)
	cerr := tmp.Close()
	if err != nil || cerr != nil {
		os.Remove(tmpName)
		if err != nil {
			return err
		}
		return cerr
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dstDir, dstName)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
