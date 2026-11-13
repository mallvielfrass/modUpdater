package mods

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"modupdater/internal/store"
)

func jarBytes(t *testing.T, id, version, name, desc string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("fabric.mod.json")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"id": id, "version": version, "name": name, "description": desc})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeJar(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func newMods(t *testing.T) (*store.DB, *Service, string, string) {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	modsDir := filepath.Join(root, "mods")
	if err := os.MkdirAll(filepath.Join(data, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(modsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(data, "modupdater.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, New(db, data), data, modsDir
}

func TestScanKeepsFieldsWhenHashChanges(t *testing.T) {
	db, svc, _, modsDir := newMods(t)
	writeJar(t, filepath.Join(modsDir, "alpha.jar"), jarBytes(t, "alpha", "1.0.0", "Meta", "FromJar"))
	res, err := svc.SetModsDir(modsDir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 {
		t.Fatalf("%+v", res)
	}
	rows, err := db.ListMods(false)
	if err != nil || len(rows) != 1 {
		t.Fatal(err, len(rows))
	}
	name := "Custom"
	desc := "Hello"
	client := true
	if _, err := svc.Update(rows[0].ID, Upload{Name: &name, Description: &desc, Client: &client}); err != nil {
		t.Fatal(err)
	}
	before, err := db.CountHistory()
	if err != nil || before != 1 {
		t.Fatalf("history %d %v", before, err)
	}
	old := rows[0].SHA256
	writeJar(t, filepath.Join(modsDir, "alpha.jar"), jarBytes(t, "alpha", "2.0.0", "Other", "OtherDesc"))
	res, err = svc.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 {
		t.Fatalf("%+v", res)
	}
	got, err := db.GetMod(rows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Client || got.Name != "Custom" || got.Description != "Hello" || got.Version != "2.0.0" || got.SHA256 == old {
		t.Fatalf("%+v", got)
	}
	after, err := db.CountHistory()
	if err != nil || after != 2 {
		t.Fatalf("history %d %v", after, err)
	}
}

func TestScanSlugConflictLeavesRow(t *testing.T) {
	db, svc, _, modsDir := newMods(t)
	writeJar(t, filepath.Join(modsDir, "a.jar"), jarBytes(t, "alpha", "1.0.0", "Meta", "D"))
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListMods(false)
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	before := rows[0]
	hist, err := db.CountHistory()
	if err != nil {
		t.Fatal(err)
	}
	writeJar(t, filepath.Join(modsDir, "b.jar"), jarBytes(t, "alpha", "9.0.0", "Nope", "Nope"))
	res, err := svc.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 0 || res.Updated != 0 || res.Removed != 0 {
		t.Fatalf("%+v", res)
	}
	if len(res.Conflicts) != 2 || res.Conflicts[0] != "a.jar" || res.Conflicts[1] != "b.jar" {
		t.Fatalf("%v", res.Conflicts)
	}
	got, err := db.GetMod(before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != before {
		t.Fatalf("changed\n%+v\n%+v", before, got)
	}
	after, err := db.CountHistory()
	if err != nil || after != hist {
		t.Fatal(after, hist, err)
	}
}

func TestScanSameHashRenameSkipsHistory(t *testing.T) {
	db, svc, _, modsDir := newMods(t)
	body := jarBytes(t, "alpha", "1.0.0", "Meta", "D")
	writeJar(t, filepath.Join(modsDir, "a.jar"), body)
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(modsDir, "a.jar"), filepath.Join(modsDir, "b.jar")); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 0 || res.Added != 0 || res.Removed != 0 {
		t.Fatalf("%+v", res)
	}
	rows, err := db.ListMods(false)
	if err != nil || len(rows) != 1 || rows[0].Filename != "b.jar" {
		t.Fatalf("%v %v", rows, err)
	}
	n, err := db.CountHistory()
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestAddSlugExistsAndUntrackedConflict(t *testing.T) {
	db, svc, _, modsDir := newMods(t)
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	first := jarBytes(t, "alpha", "1.0.0", "Meta", "D")
	m, err := svc.Add(Upload{Filename: "A.jar", File: bytesReadCloser(first)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Add(Upload{Filename: "B.jar", File: bytesReadCloser(jarBytes(t, "alpha", "2.0.0", "X", "Y"))})
	if err != ErrSlugExists {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(modsDir, "A.jar")); err != nil {
		t.Fatal(err)
	}
	plain := jarBytes(t, "plain", "1.0.0", "P", "P")
	path := filepath.Join(modsDir, "Plain.jar")
	writeJar(t, path, plain)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Add(Upload{Filename: "Plain.jar", File: bytesReadCloser(jarBytes(t, "other", "1.0.0", "O", "O"))})
	if err != ErrConflict {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("untracked jar changed", err)
	}
	got, err := db.GetMod(m.ID)
	if err != nil || got.Slug != "alpha" {
		t.Fatal(err, got)
	}
}

type bufCloser struct{ *bytes.Reader }

func (bufCloser) Close() error { return nil }

func bytesReadCloser(b []byte) bufCloser { return bufCloser{bytes.NewReader(b)} }

func TestJarDescriptionArrayAndToml(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("fabric.mod.json")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"id": "arr", "version": "1", "name": "N", "description": []string{"a", "b"}})
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp(t.TempDir(), "j-*.jar")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	meta, err := ReadMeta(f, "j.jar")
	f.Close()
	if err != nil || meta.Slug != "arr" || meta.Description != "a\nb" {
		t.Fatalf("%+v %v", meta, err)
	}

	buf.Reset()
	zw = zip.NewWriter(&buf)
	w, err = zw.Create("META-INF/mods.toml")
	if err != nil {
		t.Fatal(err)
	}
	toml := "modLoader=\"javafml\"\n[[mods]]\nmodId=\"forgeid\"\nversion=\"3\"\ndisplayName=\"Forge Name\"\ndescription=\"Hi\"\n[[dependencies.forgeid]]\nmodId=\"forge\"\n"
	if _, err := w.Write([]byte(toml)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f, err = os.CreateTemp(t.TempDir(), "t-*.jar")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	meta, err = ReadMeta(f, "t.jar")
	f.Close()
	if err != nil || meta.Slug != "forgeid" || meta.Version != "3" || meta.Name != "Forge Name" || meta.Description != "Hi" {
		t.Fatalf("%+v %v", meta, err)
	}
}
