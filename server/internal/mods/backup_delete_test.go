package mods

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"modupdater/internal/chunkbackup"
	"modupdater/internal/s3store"
	"modupdater/internal/store"
)

func TestDeleteKeepsSharedChunkAndBlocksMilestone(t *testing.T) {
	_, svc, data, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=world\n")
	mustMkdir(t, filepath.Join(root, "world"))
	big := make([]byte, chunkbackup.ChunkMax+4096)
	for i := range big {
		big[i] = byte(i*17 + 3)
	}
	if err := os.WriteFile(filepath.Join(root, "world", "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	writeServerFile(t, filepath.Join(root, "world", "keep.txt"), "stay")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc.SetWorldHold(func(copy func() error) error {
		calls++
		return copy()
	})
	mile, err := svc.Milestone()
	if err != nil {
		t.Fatal(err)
	}
	big[len(big)-1] ^= 0x5a
	if err := os.WriteFile(filepath.Join(root, "world", "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := svc.ManualSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	backupRoot := filepath.Join(data, "backups")
	mileMan, err := chunkbackup.LoadManifest(chunkbackup.ManifestPath(backupRoot, mile.ID))
	if err != nil {
		t.Fatal(err)
	}
	snapMan, err := chunkbackup.LoadManifest(chunkbackup.ManifestPath(backupRoot, snap.ID))
	if err != nil {
		t.Fatal(err)
	}
	sharedSet := chunkbackup.HashSet(mileMan.Files)
	var shared, unique string
	for hash := range chunkbackup.HashSet(snapMan.Files) {
		if _, ok := sharedSet[hash]; ok {
			shared = hash
		} else {
			unique = hash
		}
	}
	if shared == "" || unique == "" {
		t.Fatalf("shared %q unique %q", shared, unique)
	}
	st, err := chunkbackup.Open(filepath.Join(backupRoot, "chunks"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.ChunkPath(shared); !ok {
		t.Fatal("shared missing before delete")
	}
	if _, ok := st.ChunkPath(unique); !ok {
		t.Fatal("unique missing before delete")
	}
	blob := &memBlob{}
	svc.s3Build = func(store.S3Settings) (s3store.Blob, error) { return blob, nil }
	if err := svc.db.SaveS3(store.S3Settings{Bucket: "box", AccessKey: "key", Secret: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.SetBackupStorage(snap.ID, "s3"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	put := func(key string) {
		t.Helper()
		if err := blob.Put(ctx, key, bytes.NewReader([]byte("x")), 1); err != nil {
			t.Fatal(err)
		}
	}
	put(s3store.ChunkKey("", shared))
	put(s3store.ChunkKey("", unique))
	put(s3store.ManifestKey("", snap.ID))
	if _, err := svc.DeleteBackup(mile.ID); !errors.Is(err, ErrMilestoneUsed) {
		t.Fatal(err)
	}
	if _, err := os.Stat(chunkbackup.ManifestPath(backupRoot, mile.ID)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(chunkbackup.ManifestPath(backupRoot, snap.ID)); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.ChunkPath(shared); !ok || calls != 2 {
		t.Fatal("milestone delete touched data", calls)
	}
	list, err := svc.DeleteBackup(snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.ChunkPath(shared); !ok {
		t.Fatal("shared chunk deleted")
	}
	if _, ok := st.ChunkPath(unique); ok {
		t.Fatal("unique chunk kept")
	}
	if _, err := os.Stat(chunkbackup.ManifestPath(backupRoot, snap.ID)); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(chunkbackup.ManifestPath(backupRoot, mile.ID)); err != nil {
		t.Fatal(err)
	}
	stillShared, err := blob.Exists(ctx, s3store.ChunkKey("", shared))
	if err != nil || !stillShared {
		t.Fatal(stillShared, err)
	}
	gone, err := blob.Exists(ctx, s3store.ChunkKey("", unique))
	if err != nil || gone {
		t.Fatal(gone, err)
	}
	sawMile := false
	for _, b := range list {
		if b.ID == snap.ID {
			t.Fatal("snapshot row remains")
		}
		if b.ID == mile.ID {
			sawMile = true
		}
	}
	if !sawMile {
		t.Fatal("milestone row gone")
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
