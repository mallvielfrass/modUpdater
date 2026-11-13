package mods

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"modupdater/internal/chunkbackup"
	"modupdater/internal/s3store"
	"modupdater/internal/store"
)

type memBlob struct {
	mu  sync.Mutex
	obj map[string][]byte
}

func (m *memBlob) HeadBucket(context.Context) error { return nil }
func (m *memBlob) ListPrefix(context.Context, string) error {
	return nil
}
func (m *memBlob) Put(_ context.Context, key string, body io.Reader, _ int64) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.obj == nil {
		m.obj = map[string][]byte{}
	}
	m.obj[key] = b
	return nil
}
func (m *memBlob) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.obj, key)
	return nil
}
func (m *memBlob) Exists(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.obj[key]
	return ok, nil
}
func (m *memBlob) Open(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.obj[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func TestS3UploadRemovesLocalChunk(t *testing.T) {
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=world\n")
	mustMkdir(t, filepath.Join(root, "world"))
	payload := bytes.Repeat([]byte("abc"), 30000)
	writeServerFile(t, filepath.Join(root, "world", "w.bin"), string(payload))
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	blob := &memBlob{}
	svc.s3Build = func(store.S3Settings) (s3store.Blob, error) { return blob, nil }
	if err := svc.db.SaveS3(store.S3Settings{Bucket: "box", AccessKey: "key", Secret: "secret-value"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetBackupDest("s3"); err != nil {
		t.Fatal(err)
	}
	pub, err := svc.S3Public()
	if err != nil || !pub.SecretSet || pub.AccessKey != "key" || strings.Contains(pub.Bucket, "secret") {
		t.Fatalf("%+v", pub)
	}
	got, err := svc.Milestone()
	if err != nil {
		t.Fatal(err)
	}
	if got.Storage != "disk" || got.Size <= 0 || len(got.SHA256) != 64 {
		t.Fatalf("%+v", got)
	}
	if err := svc.syncS3Once(); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListBackups()
	if err != nil || list[0].Storage != "s3" || list[0].Size != got.Size || list[0].SHA256 != got.SHA256 {
		t.Fatalf("%v %+v", err, list)
	}
	st, err := svc.openChunks()
	if err != nil {
		t.Fatal(err)
	}
	manPath := filepath.Join(svc.dataDir, "backups", "manifests", got.ID+".json")
	if _, err := os.Stat(manPath); err != nil {
		t.Fatal(err)
	}
	man, err := chunkbackup.LoadManifest(manPath)
	if err != nil || len(man.Files) == 0 {
		t.Fatal(err)
	}
	saw := false
	for _, f := range man.Files {
		for _, hash := range f.Chunks {
			saw = true
			if _, ok := st.ChunkPath(hash); ok {
				t.Fatal("local chunk remains")
			}
			body, err := st.Get(hash)
			if err != nil || len(body) == 0 {
				t.Fatal(err, len(body))
			}
		}
	}
	if !saw {
		t.Fatal("no chunks")
	}
	if strings.Contains(string(mustJSON(pub)), "secret-value") {
		t.Fatal("secret leaked")
	}
}

func mustJSON(v store.S3Public) []byte {
	return []byte(v.Endpoint + v.Region + v.Bucket + v.Prefix + v.AccessKey)
}

func TestLocalDestKeepsChunks(t *testing.T) {
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=world\n")
	mustMkdir(t, filepath.Join(root, "world"))
	writeServerFile(t, filepath.Join(root, "world", "w.txt"), "map")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	blob := &memBlob{}
	svc.s3Build = func(store.S3Settings) (s3store.Blob, error) { return blob, nil }
	if err := svc.db.SaveS3(store.S3Settings{Bucket: "box", AccessKey: "key", Secret: "secret-value"}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Milestone()
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.syncS3Once(); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListBackups()
	if err != nil || list[0].Storage != "disk" {
		t.Fatalf("%v %+v", err, list)
	}
	man, err := chunkbackup.LoadManifest(filepath.Join(svc.dataDir, "backups", "manifests", got.ID+".json"))
	if err != nil || len(man.Files) == 0 {
		t.Fatal(err)
	}
	st, err := chunkbackup.Open(filepath.Join(svc.dataDir, "backups", "chunks"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range man.Files {
		for _, hash := range f.Chunks {
			if _, ok := st.ChunkPath(hash); !ok {
				t.Fatal("local chunk deleted")
			}
		}
	}
	if len(blob.obj) != 0 {
		t.Fatal(len(blob.obj))
	}
}

func TestS3DestWithoutKeys(t *testing.T) {
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=world\n")
	mustMkdir(t, filepath.Join(root, "world"))
	writeServerFile(t, filepath.Join(root, "world", "w.txt"), "map")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc.SetWorldHold(func(copy func() error) error {
		calls++
		return copy()
	})
	if err := svc.SetBackupDest("s3"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Milestone(); !errors.Is(err, ErrS3Config) || calls != 0 {
		t.Fatal(err, calls)
	}
}

func TestCheckS3NeedsKeys(t *testing.T) {
	_, svc, _, _ := newMods(t)
	if _, err := svc.CheckS3(store.S3Settings{}); !errors.Is(err, ErrS3Config) {
		t.Fatal(err)
	}
}
