package mods

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidDest(t *testing.T) {
	good := []string{"config/jei/jei.toml", "shaderpacks/ Complementary", "options.txt", "a"}
	bad := []string{"", "/etc/passwd", "a/../b", "../x", "./a", "a/./b", "a//b", `a\b`, "C:/x", "a:b", "a/\x00/b", ".", "..", "a/..", "/"}
	for _, dest := range good {
		if !ValidDest(dest) {
			t.Fatalf("want valid %q", dest)
		}
	}
	for _, dest := range bad {
		if ValidDest(dest) {
			t.Fatalf("want invalid %q", dest)
		}
	}
}

func TestScanSkipsClientOnly(t *testing.T) {
	db, svc, data, modsDir := newMods(t)
	client := false
	only := true
	m, err := svc.Add(Upload{
		Filename:   "Only.jar",
		File:       bytesReadCloser(jarBytes(t, "only", "1.0.0", "Only", "D")),
		Client:     &client,
		ClientOnly: &only,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Client || !m.ClientOnly {
		t.Fatalf("%+v", m)
	}
	if _, err := os.Stat(filepath.Join(data, "client_mods", "Only.jar")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(modsDir, "Only.jar")); !os.IsNotExist(err) {
		t.Fatal("jar stored in mods dir", err)
	}
	off := false
	got, err := svc.Update(m.ID, Upload{Client: &off, ClientOnly: &off, Name: strPtr("Kept")})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Client || !got.ClientOnly || got.Name != "Kept" {
		t.Fatalf("%+v", got)
	}
	body := jarBytes(t, "only", "2.0.0", "Only", "D2")
	updated, err := svc.Update(m.ID, Upload{Filename: "Only.jar", File: bytesReadCloser(body)})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.ClientOnly || updated.Version != "2.0.0" {
		t.Fatalf("%+v", updated)
	}
	if _, err := os.Stat(filepath.Join(data, "client_mods", "Only.jar")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(modsDir, "Only.jar")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	writeJar(t, filepath.Join(modsDir, "server.jar"), jarBytes(t, "server", "1.0.0", "Server", "D"))
	res, err := svc.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 0 {
		t.Fatalf("%+v", res)
	}
	kept, err := db.GetMod(m.ID)
	if err != nil || !kept.ClientOnly || !kept.Client || kept.Filename != "Only.jar" {
		t.Fatal(err, kept)
	}
	rows, err := db.ListMods(false)
	if err != nil || len(rows) != 2 {
		t.Fatal(err, len(rows))
	}
}

func strPtr(s string) *string { return &s }
