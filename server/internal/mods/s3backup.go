package mods

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modupdater/internal/chunkbackup"
	"modupdater/internal/s3store"
	"modupdater/internal/store"
)

var (
	ErrS3Access = errors.New("Нет доступа к бакету.")
	ErrS3Config = errors.New("Укажите бакет и ключи.")
)

func (s *Service) RunS3() {
	for {
		if err := s.syncS3Once(); err != nil {
			time.Sleep(15 * time.Second)
			continue
		}
		time.Sleep(3 * time.Second)
	}
}

func (s *Service) SetBackupDest(dest string) error {
	if dest != "disk" && dest != "s3" {
		return ErrS3Config
	}
	return s.db.SetBackupDest(dest)
}

func (s *Service) SaveS3(in store.S3Settings) (store.S3Public, error) {
	if err := s.db.SaveS3(in); err != nil {
		return store.S3Public{}, err
	}
	return s.db.S3Public()
}

func (s *Service) S3Public() (store.S3Public, error) {
	return s.db.S3Public()
}

func (s *Service) CheckS3(in store.S3Settings) (string, error) {
	if in.Secret == "" {
		saved, err := s.db.S3()
		if err != nil {
			return "", err
		}
		in.Secret = saved.Secret
	}
	in.Endpoint = strings.TrimSpace(in.Endpoint)
	in.Region = strings.TrimSpace(in.Region)
	in.Bucket = strings.TrimSpace(in.Bucket)
	in.Prefix = strings.Trim(strings.TrimSpace(in.Prefix), "/")
	in.AccessKey = strings.TrimSpace(in.AccessKey)
	if !in.Ready() {
		return "", ErrS3Config
	}
	client, err := s.blobClient(in)
	if err != nil {
		return "", ErrS3Access
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s3store.Probe(ctx, client, in.Prefix); err != nil {
		return "", ErrS3Access
	}
	return "Доступ к бакету есть.", nil
}

func (s *Service) blobClient(cfg store.S3Settings) (s3store.Blob, error) {
	if s.s3Build != nil {
		return s.s3Build(cfg)
	}
	return s3store.New(s3store.Settings{
		Endpoint: cfg.Endpoint, Region: cfg.Region, Bucket: cfg.Bucket,
		Prefix: cfg.Prefix, AccessKey: cfg.AccessKey, Secret: cfg.Secret,
	})
}

func (s *Service) openChunks() (*chunkbackup.Store, error) {
	root, err := s.backupRoot()
	if err != nil {
		return nil, err
	}
	st, err := chunkbackup.Open(filepath.Join(root, "chunks"))
	if err != nil {
		return nil, err
	}
	cfg, err := s.db.S3()
	if err != nil || !cfg.Ready() {
		return st, nil
	}
	client, err := s.blobClient(cfg)
	if err != nil {
		return st, nil
	}
	prefix := cfg.Prefix
	st.SetRemote(func(hash string) (io.ReadCloser, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		rc, err := client.Open(ctx, s3store.ChunkKey(prefix, hash))
		if err != nil {
			cancel()
			return nil, err
		}
		return &cancelRead{ReadCloser: rc, cancel: cancel}, nil
	})
	return st, nil
}

type cancelRead struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelRead) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func (s *Service) syncS3Once() error {
	dest, err := s.db.BackupDest()
	if err != nil || dest != "s3" {
		return err
	}
	cfg, err := s.db.S3()
	if err != nil || !cfg.Ready() {
		return nil
	}
	client, err := s.blobClient(cfg)
	if err != nil {
		return err
	}
	list, err := s.db.PendingS3Backups()
	if err != nil || len(list) == 0 {
		return err
	}
	return s.pushBackup(client, cfg, list[0])
}

func (s *Service) pushBackup(client s3store.Blob, cfg store.S3Settings, b store.Backup) error {
	root, err := s.backupRoot()
	if err != nil {
		return err
	}
	man, err := chunkbackup.LoadManifest(chunkbackup.ManifestPath(root, b.ID))
	if err != nil {
		return err
	}
	var parent []chunkbackup.FileEntry
	if man.Parent != "" {
		pm, err := chunkbackup.LoadManifest(chunkbackup.ManifestPath(root, man.Parent))
		if err != nil {
			return err
		}
		parent = pm.Files
	}
	st, err := chunkbackup.Open(filepath.Join(root, "chunks"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	for _, hash := range chunkbackup.OwnHashes(man.Files, parent) {
		if err := s.pushChunk(ctx, client, st, cfg.Prefix, hash); err != nil {
			return err
		}
	}
	raw, err := os.ReadFile(chunkbackup.ManifestPath(root, b.ID))
	if err != nil {
		return err
	}
	if err := client.Put(ctx, s3store.ManifestKey(cfg.Prefix, b.ID), strings.NewReader(string(raw)), int64(len(raw))); err != nil {
		return err
	}
	return s.db.SetBackupStorage(b.ID, "s3")
}

func (s *Service) pushChunk(ctx context.Context, client s3store.Blob, st *chunkbackup.Store, prefix, hash string) error {
	key := s3store.ChunkKey(prefix, hash)
	path, ok := st.ChunkPath(hash)
	if !ok {
		exists, err := client.Exists(ctx, key)
		if err != nil {
			return err
		}
		if !exists {
			return errors.New("chunk missing")
		}
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	putErr := client.Put(ctx, key, f, info.Size())
	f.Close()
	if putErr != nil {
		return putErr
	}
	exists, err := client.Exists(ctx, key)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("chunk missing")
	}
	return os.Remove(path)
}
