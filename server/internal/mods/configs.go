package mods

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"modupdater/internal/store"
)

func ValidDest(dest string) bool {
	if dest == "" || strings.ContainsAny(dest, "\\\x00:") {
		return false
	}
	if strings.HasPrefix(dest, "/") {
		return false
	}
	for _, p := range strings.Split(dest, "/") {
		if p == "" || p == "." || p == ".." {
			return false
		}
	}
	return true
}

func SafeConfigName(raw string) (string, error) {
	raw = strings.ReplaceAll(raw, "\\", "/")
	name := path.Base(raw)
	if name == "" || name == "." || name == ".." || len(name) > 180 {
		return "", ErrBadPath
	}
	if strings.ContainsAny(name, "/\\\x00\"\r\n;") {
		return "", ErrBadPath
	}
	return name, nil
}

func (s *Service) ModConfigs(modID string) ([]store.ModConfig, error) {
	return s.db.ListConfigs(modID)
}

func (s *Service) AllConfigs() (map[string][]store.ModConfig, error) {
	return s.db.ConfigsByMod()
}

func (s *Service) deleteRow(row store.Mod, h store.History) error {
	cfgs, err := s.db.ListConfigs(row.ID)
	if err != nil {
		return err
	}
	if err := s.db.DeleteModWithHistory(row.ID, h); err != nil {
		return err
	}
	for _, c := range cfgs {
		if err := s.removeBlob(c.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) configDir() (string, error) {
	return s.dataSubdir("client_configs")
}

func (s *Service) removeBlob(id string) error {
	if !store.ValidID(id) {
		return ErrBadPath
	}
	dir, err := s.configDir()
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(dir, id+".bin"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Service) AddConfig(modID, dest, kind, filename string, r io.Reader) (store.ModConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !store.ValidID(modID) {
		return store.ModConfig{}, ErrNotFound
	}
	m, err := s.db.GetMod(modID)
	if errors.Is(err, store.ErrNotFound) {
		return store.ModConfig{}, ErrNotFound
	}
	if err != nil {
		return store.ModConfig{}, err
	}
	if !m.Client {
		return store.ModConfig{}, ErrForbidden
	}
	if kind != "file" && kind != "dir" {
		return store.ModConfig{}, ErrInvalidJSON
	}
	if !ValidDest(dest) {
		return store.ModConfig{}, ErrInvalidDest
	}
	name, err := SafeConfigName(filename)
	if err != nil {
		return store.ModConfig{}, ErrInvalidFilename
	}
	id, err := store.NewID()
	if err != nil {
		return store.ModConfig{}, err
	}
	dir, err := s.configDir()
	if err != nil {
		return store.ModConfig{}, err
	}
	tmp, sum, size, err := writeTemp(dir, r, MaxConfig)
	if err != nil {
		return store.ModConfig{}, err
	}
	final := id + ".bin"
	if err := commitTemp(tmp, dir, final); err != nil {
		return store.ModConfig{}, err
	}
	cfg := store.ModConfig{
		ID: id, ModID: modID, Dest: dest, Kind: kind, Filename: name,
		SHA256: sum, Size: size, UpdatedAt: store.Now(),
	}
	if err := s.db.InsertConfig(cfg); err != nil {
		os.Remove(filepath.Join(dir, final))
		return store.ModConfig{}, err
	}
	return cfg, nil
}

func (s *Service) DeleteConfig(modID, configID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !store.ValidID(modID) || !store.ValidID(configID) {
		return ErrNotFound
	}
	if _, err := s.db.GetConfig(modID, configID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	if err := s.db.DeleteConfig(modID, configID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return s.removeBlob(configID)
}

func (s *Service) OpenConfigFile(modID, configID string) (*os.File, string, error) {
	if !store.ValidID(modID) || !store.ValidID(configID) {
		return nil, "", ErrNotFound
	}
	m, err := s.db.GetMod(modID)
	if err != nil || !clientVisible(m) {
		return nil, "", ErrNotFound
	}
	cfg, err := s.db.GetConfig(modID, configID)
	if err != nil {
		return nil, "", ErrNotFound
	}
	dir, err := s.configDir()
	if err != nil {
		return nil, "", ErrNotFound
	}
	f, err := OpenPlain(dir, configID+".bin")
	if err != nil {
		return nil, "", ErrNotFound
	}
	return f, cfg.Filename, nil
}
