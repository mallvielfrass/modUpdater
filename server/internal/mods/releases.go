package mods

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"modupdater/internal/store"
)

const (
	linuxReleaseName   = "modupdater-linux"
	windowsReleaseName = "modupdater-windows.exe"
)

var versionRe = regexp.MustCompile(`^[0-9A-Za-z._+-]{1,32}$`)

func ValidVersion(v string) bool {
	if v == "." || v == ".." {
		return false
	}
	return versionRe.MatchString(v)
}

func releaseBinary(osName string) (string, error) {
	switch osName {
	case "linux":
		return linuxReleaseName, nil
	case "windows":
		return windowsReleaseName, nil
	default:
		return "", ErrNotFound
	}
}

func (s *Service) makeReleaseDir(version string) (string, error) {
	if !ValidVersion(version) {
		return "", ErrInvalidVersion
	}
	root, err := s.dataSubdir("releases")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	real = filepath.Clean(real)
	if !pathInside(root, real) {
		return "", ErrInvalidVersion
	}
	return real, nil
}

func (s *Service) UploadRelease(version string, linux, windows io.Reader) (store.ClientRelease, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ValidVersion(version) {
		return store.ClientRelease{}, false, ErrInvalidVersion
	}
	cur, err := s.db.GetRelease(version)
	if err == nil && cur.Status != "dev" {
		return store.ClientRelease{}, false, ErrConflict
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.ClientRelease{}, false, err
	}
	dir, err := s.makeReleaseDir(version)
	if err != nil {
		return store.ClientRelease{}, false, err
	}
	lsum, lsize, err := writeNamed(dir, linuxReleaseName, linux)
	if err != nil {
		return store.ClientRelease{}, false, err
	}
	wsum, wsize, err := writeNamed(dir, windowsReleaseName, windows)
	if err != nil {
		return store.ClientRelease{}, false, err
	}
	rel := store.ClientRelease{
		Version: version, Status: "dev",
		LinuxSHA256: lsum, LinuxSize: lsize,
		WindowsSHA256: wsum, WindowsSize: wsize,
		CreatedAt: store.Now(),
	}
	created, err := s.db.UpsertDevRelease(rel)
	if errors.Is(err, store.ErrConflict) {
		return store.ClientRelease{}, false, ErrConflict
	}
	if err != nil {
		return store.ClientRelease{}, false, err
	}
	return rel, created, nil
}

func writeNamed(dir, name string, r io.Reader) (string, int64, error) {
	tmp, sum, size, err := writeTemp(dir, r, MaxRelease)
	if err != nil {
		return "", 0, err
	}
	if err := commitTemp(tmp, dir, name); err != nil {
		return "", 0, err
	}
	return sum, size, nil
}

func (s *Service) PublishRelease(version string) (store.ClientRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ValidVersion(version) {
		return store.ClientRelease{}, ErrNotFound
	}
	if _, err := s.db.GetRelease(version); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.ClientRelease{}, ErrNotFound
		}
		return store.ClientRelease{}, err
	}
	root, err := s.dataSubdir("releases")
	if err != nil {
		return store.ClientRelease{}, err
	}
	dir := filepath.Join(root, version)
	if !regularNonEmpty(dir, linuxReleaseName) || !regularNonEmpty(dir, windowsReleaseName) {
		return store.ClientRelease{}, ErrConflict
	}
	rel, err := s.db.PublishRelease(version)
	if errors.Is(err, store.ErrNotFound) {
		return store.ClientRelease{}, ErrNotFound
	}
	if errors.Is(err, store.ErrConflict) {
		return store.ClientRelease{}, ErrConflict
	}
	return rel, err
}

func regularNonEmpty(dir, name string) bool {
	f, err := OpenPlain(dir, name)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	return err == nil && st.Size() > 0
}

func (s *Service) DeleteRelease(version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ValidVersion(version) {
		return ErrNotFound
	}
	if err := s.db.DeleteDevRelease(version); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if errors.Is(err, store.ErrConflict) {
			return ErrConflict
		}
		return err
	}
	root, err := s.dataSubdir("releases")
	if err != nil {
		return err
	}
	dir := filepath.Join(root, version)
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	real = filepath.Clean(real)
	if !pathInside(root, real) {
		return ErrBadPath
	}
	return os.RemoveAll(real)
}

func (s *Service) ListReleases() (string, []store.ClientRelease, error) {
	active, err := s.db.ActiveClientVersion()
	if err != nil {
		return "", nil, err
	}
	list, err := s.db.ListReleases()
	if err != nil {
		return "", nil, err
	}
	return active, list, nil
}

func (s *Service) Latest() (store.ClientRelease, bool, error) {
	active, err := s.db.ActiveClientVersion()
	if err != nil {
		return store.ClientRelease{}, false, err
	}
	if active == "" {
		return store.ClientRelease{}, false, nil
	}
	rel, err := s.db.GetRelease(active)
	if errors.Is(err, store.ErrNotFound) {
		return store.ClientRelease{}, false, nil
	}
	if err != nil {
		return store.ClientRelease{}, false, err
	}
	if rel.Status != "released" {
		return store.ClientRelease{}, false, nil
	}
	return rel, true, nil
}

func (s *Service) OpenReleaseFile(version, osName string, public bool) (*os.File, string, error) {
	name, err := releaseBinary(osName)
	if err != nil {
		return nil, "", ErrNotFound
	}
	if !ValidVersion(version) {
		return nil, "", ErrNotFound
	}
	rel, err := s.db.GetRelease(version)
	if err != nil {
		return nil, "", ErrNotFound
	}
	if public {
		active, aerr := s.db.ActiveClientVersion()
		if aerr != nil || active != version || rel.Status != "released" {
			return nil, "", ErrNotFound
		}
	}
	root, err := s.dataSubdir("releases")
	if err != nil {
		return nil, "", ErrNotFound
	}
	dir := filepath.Join(root, version)
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, "", ErrNotFound
	}
	real = filepath.Clean(real)
	if !pathInside(root, real) {
		return nil, "", ErrNotFound
	}
	f, err := OpenPlain(real, name)
	if err != nil {
		return nil, "", ErrNotFound
	}
	return f, name, nil
}
