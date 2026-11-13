package chunkbackup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/zstd"
)

var ErrChunk = errors.New("chunk")

type Store struct {
	dir    string
	remote func(hash string) (io.ReadCloser, error)
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func validHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for _, c := range h {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *Store) path(hash string) string {
	return filepath.Join(s.dir, hash[:2], hash[2:])
}

func (s *Store) Has(hash string) bool {
	if !validHash(hash) {
		return false
	}
	st, err := os.Lstat(s.path(hash))
	return err == nil && st.Mode().IsRegular() && st.Mode()&os.ModeSymlink == 0
}

func (s *Store) Put(chunk []byte) (string, int64, error) {
	if len(chunk) == 0 {
		return "", 0, ErrChunk
	}
	sum := sha256.Sum256(chunk)
	hash := hex.EncodeToString(sum[:])
	if s.Has(hash) {
		return hash, 0, nil
	}
	if err := s.writeNew(hash, chunk); err != nil {
		return "", 0, err
	}
	return hash, int64(len(chunk)), nil
}

func (s *Store) writeNew(hash string, chunk []byte) error {
	dir := filepath.Dir(s.path(hash))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "part-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	enc, err := zstd.NewWriter(tmp)
	if err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	_, werr := enc.Write(chunk)
	cerr := enc.Close()
	ferr := tmp.Close()
	if werr != nil || cerr != nil || ferr != nil {
		os.Remove(tmpName)
		if werr != nil {
			return werr
		}
		if cerr != nil {
			return cerr
		}
		return ferr
	}
	if s.Has(hash) {
		os.Remove(tmpName)
		return nil
	}
	if err := os.Rename(tmpName, s.path(hash)); err != nil {
		os.Remove(tmpName)
		if s.Has(hash) {
			return nil
		}
		return err
	}
	return nil
}

func (s *Store) AddPlain(expect string, r io.Reader) error {
	if !validHash(expect) {
		return ErrChunk
	}
	if s.Has(expect) {
		_, err := io.Copy(io.Discard, r)
		return err
	}
	dir := filepath.Dir(s.path(expect))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "part-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	h := sha256.New()
	enc, err := zstd.NewWriter(tmp)
	if err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	_, werr := io.Copy(io.MultiWriter(enc, h), r)
	cerr := enc.Close()
	ferr := tmp.Close()
	if werr != nil || cerr != nil || ferr != nil {
		os.Remove(tmpName)
		if werr != nil {
			return werr
		}
		if cerr != nil {
			return cerr
		}
		return ferr
	}
	if hex.EncodeToString(h.Sum(nil)) != expect {
		os.Remove(tmpName)
		return ErrChunk
	}
	if s.Has(expect) {
		os.Remove(tmpName)
		return nil
	}
	if err := os.Rename(tmpName, s.path(expect)); err != nil {
		os.Remove(tmpName)
		if s.Has(expect) {
			return nil
		}
		return err
	}
	return nil
}

func (s *Store) SetRemote(fn func(string) (io.ReadCloser, error)) {
	s.remote = fn
}

func (s *Store) ChunkPath(hash string) (string, bool) {
	if !s.Has(hash) {
		return "", false
	}
	return s.path(hash), true
}

func (s *Store) Get(hash string) ([]byte, error) {
	if !validHash(hash) {
		return nil, ErrChunk
	}
	if s.Has(hash) {
		f, err := os.Open(s.path(hash))
		if err != nil && s.remote == nil {
			return nil, err
		}
		if err == nil {
			b, decErr := decodeChunk(f, hash)
			f.Close()
			if decErr == nil || s.remote == nil {
				return b, decErr
			}
		}
	}
	if s.remote == nil {
		return nil, ErrChunk
	}
	rc, err := s.remote(hash)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return decodeChunk(rc, hash)
}

func decodeChunk(r io.Reader, hash string) ([]byte, error) {
	zr, err := zstd.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	b, err := io.ReadAll(io.LimitReader(zr, ChunkMax+1))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || len(b) > ChunkMax {
		return nil, ErrChunk
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != hash {
		return nil, ErrChunk
	}
	return b, nil
}

func (s *Store) PutFile(path string, onBytes func(int64)) ([]string, int64, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer f.Close()
	c := NewChunker(f)
	var hashes []string
	var fresh, size int64
	for {
		chunk, err := c.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, 0, err
		}
		if len(chunk) == 0 {
			return nil, 0, 0, ErrChunk
		}
		if onBytes != nil {
			onBytes(int64(len(chunk)))
		}
		size += int64(len(chunk))
		hash, n, err := s.Put(chunk)
		if err != nil {
			return nil, 0, 0, err
		}
		fresh += n
		hashes = append(hashes, hash)
	}
	return hashes, fresh, size, nil
}

func (s *Store) DiskSize(files []FileEntry, skip map[string]struct{}) (int64, error) {
	seen := map[string]struct{}{}
	var n int64
	for _, f := range files {
		for _, hash := range f.Chunks {
			if _, ok := skip[hash]; ok {
				continue
			}
			if _, ok := seen[hash]; ok {
				continue
			}
			seen[hash] = struct{}{}
			if !validHash(hash) {
				return 0, ErrChunk
			}
			st, err := os.Lstat(s.path(hash))
			if err != nil {
				return 0, err
			}
			if !st.Mode().IsRegular() {
				return 0, ErrChunk
			}
			n += st.Size()
		}
	}
	return n, nil
}
