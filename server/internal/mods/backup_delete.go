package mods

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"modupdater/internal/chunkbackup"
	"modupdater/internal/s3store"
	"modupdater/internal/store"
)

func (s *Service) DeleteBackup(id string) ([]store.Backup, error) {
	if !s.mu.TryLock() {
		return nil, ErrBackupRunning
	}
	defer s.mu.Unlock()
	if !store.ValidID(id) {
		return nil, ErrBackupMissing
	}
	b, err := s.db.GetBackup(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrBackupMissing
		}
		return nil, err
	}
	if b.Kind == backupKindMilestone {
		n, err := s.db.CountChildSnapshots(id)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, ErrMilestoneUsed
		}
	}
	root, err := s.backupRoot()
	if err != nil {
		return nil, mapSnapshot(err)
	}
	if err := s.removeBackupBytes(root, b); err != nil {
		return nil, err
	}
	if err := s.db.DeleteBackupRow(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrBackupMissing
		}
		return nil, err
	}
	return s.ListBackups()
}

func (s *Service) removeBackupBytes(root string, b store.Backup) error {
	if legacyBackup(root, b.ID) {
		if err := os.RemoveAll(filepath.Join(root, b.ID)); err != nil {
			return mapSnapshot(err)
		}
	}
	own, _, err := manifestHashes(root, b.ID)
	if err != nil {
		return mapSnapshot(err)
	}
	keep, err := s.otherChunkHashes(root, b.ID)
	if err != nil {
		return mapSnapshot(err)
	}
	var drop []string
	for hash := range own {
		if _, used := keep[hash]; used {
			continue
		}
		drop = append(drop, hash)
	}
	cfg, err := s.db.S3()
	if err != nil {
		return err
	}
	if b.Storage == "s3" && !cfg.Ready() {
		return ErrS3Config
	}
	var client s3store.Blob
	if cfg.Ready() {
		client, err = s.blobClient(cfg)
		if err != nil {
			return ErrS3Access
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, hash := range drop {
			if err := client.Delete(ctx, s3store.ChunkKey(cfg.Prefix, hash)); err != nil {
				return ErrS3Access
			}
		}
		if err := client.Delete(ctx, s3store.ManifestKey(cfg.Prefix, b.ID)); err != nil {
			return ErrS3Access
		}
	}
	st, err := chunkbackup.Open(filepath.Join(root, "chunks"))
	if err != nil {
		return mapSnapshot(err)
	}
	for _, hash := range drop {
		path, ok := st.ChunkPath(hash)
		if !ok {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return mapSnapshot(err)
		}
	}
	man := chunkbackup.ManifestPath(root, b.ID)
	if err := os.Remove(man); err != nil && !os.IsNotExist(err) {
		return mapSnapshot(err)
	}
	return nil
}

func (s *Service) otherChunkHashes(root, skip string) (map[string]struct{}, error) {
	list, err := s.db.ListBackups()
	if err != nil {
		return nil, err
	}
	keep := map[string]struct{}{}
	for _, b := range list {
		if b.ID == skip {
			continue
		}
		hashes, _, err := manifestHashes(root, b.ID)
		if err != nil {
			return nil, err
		}
		for hash := range hashes {
			keep[hash] = struct{}{}
		}
	}
	return keep, nil
}

func manifestHashes(root, id string) (map[string]struct{}, bool, error) {
	path := chunkbackup.ManifestPath(root, id)
	st, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]struct{}{}, false, nil
		}
		return nil, false, err
	}
	if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
		return map[string]struct{}{}, false, nil
	}
	man, err := chunkbackup.LoadManifest(path)
	if err != nil {
		return nil, false, err
	}
	return chunkbackup.HashSet(man.Files), true, nil
}
