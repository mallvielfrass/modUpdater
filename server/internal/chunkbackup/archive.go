package chunkbackup

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

func WriteArchive(w io.Writer, st *Store, man Manifest, parent *Manifest) error {
	var skip map[string]struct{}
	if parent != nil {
		skip = HashSet(parent.Files)
	}
	enc, err := zstd.NewWriter(w)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(enc)
	raw, err := marshalManifest(man)
	if err != nil {
		tw.Close()
		enc.Close()
		return err
	}
	if err := writeTarBytes(tw, "manifest.json", raw); err != nil {
		tw.Close()
		enc.Close()
		return err
	}
	seen := map[string]struct{}{}
	for _, f := range man.Files {
		for _, hash := range f.Chunks {
			if _, ok := skip[hash]; ok {
				continue
			}
			if _, ok := seen[hash]; ok {
				continue
			}
			seen[hash] = struct{}{}
			data, err := st.Get(hash)
			if err != nil {
				tw.Close()
				enc.Close()
				return err
			}
			if err := writeTarBytes(tw, "chunks/"+hash, data); err != nil {
				tw.Close()
				enc.Close()
				return err
			}
		}
	}
	if err := tw.Close(); err != nil {
		enc.Close()
		return err
	}
	return enc.Close()
}

func writeTarBytes(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name:     name,
		Mode:     0o644,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatUSTAR,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func ReadArchive(r io.Reader, st *Store) (Manifest, error) {
	zr, err := zstd.NewReader(r)
	if err != nil {
		return Manifest{}, err
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	var man Manifest
	var have bool
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Manifest{}, err
		}
		name := hdr.Name
		switch {
		case name == "manifest.json":
			b, err := io.ReadAll(io.LimitReader(tr, 64<<20))
			if err != nil {
				return Manifest{}, err
			}
			if err := json.Unmarshal(b, &man); err != nil {
				return Manifest{}, ErrManifest
			}
			have = true
		case strings.HasPrefix(name, "chunks/"):
			if err := st.AddPlain(strings.TrimPrefix(name, "chunks/"), tr); err != nil {
				return Manifest{}, err
			}
		default:
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return Manifest{}, err
			}
		}
	}
	if !have {
		return Manifest{}, ErrManifest
	}
	return man, nil
}

func RestoreStored(root, id, dest string) error {
	man, err := LoadManifest(ManifestPath(root, id))
	if err != nil {
		return err
	}
	if man.Parent != "" {
		parent, err := LoadManifest(ManifestPath(root, man.Parent))
		if err != nil {
			return err
		}
		man = Apply(parent, man)
	}
	st, err := Open(filepath.Join(root, "chunks"))
	if err != nil {
		return err
	}
	return Materialize(st, man, dest)
}

func Merge(milestonePath, snapshotPath, outDir string) error {
	if milestonePath == "" || outDir == "" {
		return errors.New("не указаны файлы")
	}
	tmp, err := os.MkdirTemp("", "backup-merge-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	st, err := Open(filepath.Join(tmp, "chunks"))
	if err != nil {
		return err
	}
	mf, err := os.Open(milestonePath)
	if err != nil {
		return err
	}
	man, err := ReadArchive(mf, st)
	mf.Close()
	if err != nil {
		return err
	}
	effective := man
	if snapshotPath != "" {
		sf, err := os.Open(snapshotPath)
		if err != nil {
			return err
		}
		snap, err := ReadArchive(sf, st)
		sf.Close()
		if err != nil {
			return err
		}
		if snap.Parent != "" && man.ID != "" && snap.Parent != man.ID {
			return errors.New("Снапшот не от этой вехи.")
		}
		effective = Apply(man, snap)
	}
	return Materialize(st, effective, outDir)
}

func Materialize(st *Store, man Manifest, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for _, root := range man.Roots {
		if !validTop(root) {
			return ErrManifest
		}
		if err := os.MkdirAll(filepath.Join(dest, root), 0o755); err != nil {
			return err
		}
	}
	for _, f := range man.Files {
		top, _, ok := strings.Cut(f.Path, "/")
		if !ok || !validTop(top) {
			return ErrManifest
		}
		if err := writeRel(st, dest, f.Path, f.Mode, f.Link, f.Chunks, nil); err != nil {
			return err
		}
	}
	return nil
}

func MaterializePrefix(st *Store, man Manifest, prefix, dest string, onBytes func(int64), onFile func()) error {
	if !validTop(prefix) {
		return ErrManifest
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	pre := prefix + "/"
	for _, f := range man.Files {
		if !strings.HasPrefix(f.Path, pre) {
			continue
		}
		rel := strings.TrimPrefix(f.Path, pre)
		if err := writeRel(st, dest, rel, f.Mode, f.Link, f.Chunks, onBytes); err != nil {
			return err
		}
		if onFile != nil && os.FileMode(f.Mode)&os.ModeSymlink == 0 {
			onFile()
		}
	}
	return nil
}

func writeRel(st *Store, dest, rel string, mode uint32, link string, chunks []string, onBytes func(int64)) error {
	if !validRel(rel) {
		return ErrManifest
	}
	dest = filepath.Clean(dest)
	target := filepath.Clean(filepath.Join(dest, filepath.FromSlash(rel)))
	if !inside(dest, target) {
		return ErrManifest
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	fm := os.FileMode(mode)
	if fm&os.ModeSymlink != 0 {
		if strings.Contains(link, "\x00") {
			return ErrManifest
		}
		_ = os.Remove(target)
		return os.Symlink(link, target)
	}
	perm := fm.Perm()
	if perm == 0 {
		perm = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	for _, hash := range chunks {
		b, err := st.Get(hash)
		if err != nil {
			out.Close()
			return err
		}
		if _, err := out.Write(b); err != nil {
			out.Close()
			return err
		}
		if onBytes != nil {
			onBytes(int64(len(b)))
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(target, perm)
}
