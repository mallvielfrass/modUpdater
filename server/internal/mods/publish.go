package mods

import (
	"errors"
	"os"

	"modupdater/internal/store"
)

func (s *Service) PublicList() ([]store.Mod, error) {
	return s.db.ListMods(true)
}

func (s *Service) PublicRevoked() ([]store.Mod, error) {
	return s.db.ListRevoked()
}

func (s *Service) PublicGet(id string) (store.Mod, error) {
	if !store.ValidID(id) {
		return store.Mod{}, ErrNotFound
	}
	m, err := s.db.GetMod(id)
	if err != nil || !clientVisible(m) {
		return store.Mod{}, ErrNotFound
	}
	return m, nil
}

func (s *Service) OpenClientFile(id string) (*os.File, string, error) {
	if !store.ValidID(id) {
		return nil, "", ErrNotFound
	}
	m, err := s.db.GetMod(id)
	if err != nil || !clientVisible(m) {
		return nil, "", ErrNotFound
	}
	var dir string
	if m.ClientOnly {
		dir, err = s.clientModsDir()
	} else {
		dir, err = s.resolve()
	}
	if err != nil {
		return nil, "", ErrNotFound
	}
	safe, err := SafeJarName(m.Filename)
	if err != nil || safe != m.Filename {
		return nil, "", ErrNotFound
	}
	f, err := OpenRegular(dir, safe)
	if err != nil {
		return nil, "", ErrNotFound
	}
	return f, safe, nil
}

func (s *Service) OpenDownloadFile(id string) (*os.File, string, error) {
	if !store.ValidID(id) {
		return nil, "", ErrNotFound
	}
	m, err := s.db.GetMod(id)
	if err != nil || m.Revoked {
		return nil, "", ErrNotFound
	}
	dir, err := s.modDir(m.ClientOnly)
	if err != nil {
		return nil, "", ErrNotFound
	}
	safe, err := SafeJarName(m.Filename)
	if err != nil || safe != m.Filename {
		return nil, "", ErrNotFound
	}
	f, err := OpenRegular(dir, safe)
	if err != nil {
		return nil, "", ErrNotFound
	}
	return f, safe, nil
}

func clientVisible(m store.Mod) bool {
	return m.Client && !m.Revoked
}

func (s *Service) PublicHistory(limit int) ([]Event, error) {
	if limit < 1 || limit > 200 {
		return nil, ErrInvalidLimit
	}
	rows, err := s.db.ListPublicHistory(limit)
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, h := range rows {
		out = append(out, s.toEvent(h))
	}
	return out, nil
}

func (s *Service) OpenHistoryFile(id string) (*os.File, string, error) {
	if !store.ValidID(id) {
		return nil, "", ErrNotFound
	}
	h, err := s.db.GetHistory(id)
	if err != nil || !h.Client {
		return nil, "", ErrNotFound
	}
	m, err := s.db.GetMod(h.ModID)
	if err == nil && !m.Client {
		return nil, "", ErrNotFound
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, "", ErrNotFound
	}
	if !SafeArchiveName(h.ArchiveName) {
		return nil, "", ErrNotFound
	}
	arch, err := s.archiveDir()
	if err != nil {
		return nil, "", ErrNotFound
	}
	f, err := OpenRegular(arch, h.ArchiveName)
	if err != nil {
		return nil, "", ErrNotFound
	}
	safe, err := SafeJarName(h.Filename)
	if err != nil || safe != h.Filename {
		f.Close()
		return nil, "", ErrNotFound
	}
	return f, safe, nil
}

func (s *Service) toEvent(h store.History) Event {
	return Event{
		ID: h.ID, ModID: h.ModID, Slug: h.Slug, Name: h.Name, Action: h.Action,
		Filename: h.Filename, Version: h.Version, SHA256: h.SHA256, Size: h.Size,
		Client: h.Client, CreatedAt: h.CreatedAt, HasFile: s.historyHasFile(h.ArchiveName),
	}
}

func (s *Service) historyHasFile(name string) bool {
	if !SafeArchiveName(name) {
		return false
	}
	arch, err := s.archiveDir()
	if err != nil {
		return false
	}
	f, err := OpenRegular(arch, name)
	if err != nil {
		return false
	}
	f.Close()
	return true
}
