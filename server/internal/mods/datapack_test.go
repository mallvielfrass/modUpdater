package mods

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestDatapackUpload(t *testing.T) {
	_, svc, data, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=NewWorld\n")
	mustMkdir(t, filepath.Join(root, "NewWorld"))
	decoy := filepath.Join(root, "world", "datapacks", "decoy")
	mustMkdir(t, decoy)
	writeServerFile(t, filepath.Join(decoy, "pack.mcmeta"), "decoy")
	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "mods")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}

	packs := filepath.Join(root, "NewWorld", "datapacks")
	reject := func(name string, body []byte) {
		t.Helper()
		_, err := svc.InstallDatapack(name, bytes.NewReader(body))
		if !errors.Is(err, ErrNotDatapack) {
			t.Fatal(err)
		}
	}
	reject("nope.zip", datapackZip(t, map[string]string{"readme.txt": "no"}))
	reject("two.zip", datapackZip(t, map[string]string{
		"a/pack.mcmeta": `{"pack":{"description":"A"}}`,
		"b/pack.mcmeta": `{"pack":{"description":"B"}}`,
	}))
	if _, err := os.Stat(packs); !os.IsNotExist(err) {
		t.Fatal(err)
	}

	other := filepath.Join(packs, "other")
	mustMkdir(t, other)
	writeServerFile(t, filepath.Join(other, "pack.mcmeta"), `{"pack":{"description":"Чужой"}}`)
	writeServerFile(t, filepath.Join(other, "keep.txt"), "keep")
	reject("nope.zip", datapackZip(t, map[string]string{"data/foo.txt": "x"}))
	if readServerFile(t, filepath.Join(other, "keep.txt")) != "keep" {
		t.Fatal("other")
	}
	if _, err := os.Stat(filepath.Join(packs, "nope")); !os.IsNotExist(err) {
		t.Fatal(err)
	}

	got, err := svc.InstallDatapack("quest.zip", bytes.NewReader(datapackZip(t, map[string]string{
		"Wrapped/pack.mcmeta":  `{"pack":{"pack_format":48,"description":"Первый"}}`,
		"Wrapped/data/old.txt": "old",
	})))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "quest" || got.Description != "Первый" {
		t.Fatalf("%+v", got)
	}
	quest := filepath.Join(packs, "quest")
	if _, err := os.Stat(filepath.Join(quest, "Wrapped")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if readServerFile(t, filepath.Join(quest, "data", "old.txt")) != "old" {
		t.Fatal("old")
	}
	st, err := os.Stat(packs)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0o755 {
		t.Fatal(st, err)
	}

	got, err = svc.InstallDatapack("quest.zip", bytes.NewReader(datapackZip(t, map[string]string{
		"pack.mcmeta":  `{"pack":{"pack_format":48,"description":"Второй"}}`,
		"data/new.txt": "new",
	})))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "quest" || got.Description != "Второй" {
		t.Fatalf("%+v", got)
	}
	if _, err := os.Stat(filepath.Join(quest, "data", "old.txt")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if readServerFile(t, filepath.Join(quest, "data", "new.txt")) != "new" {
		t.Fatal("new")
	}
	if readServerFile(t, filepath.Join(other, "keep.txt")) != "keep" {
		t.Fatal("other")
	}
	if readServerFile(t, filepath.Join(modsDir, "a.txt")) != "mods" {
		t.Fatal("mods")
	}
	if readServerFile(t, filepath.Join(decoy, "pack.mcmeta")) != "decoy" {
		t.Fatal("decoy")
	}
	if _, err := os.Stat(filepath.Join(root, "config")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "backups")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	left, err := filepath.Glob(filepath.Join(data, "datapack-*.zip"))
	if err != nil || len(left) != 0 {
		t.Fatal(left, err)
	}
	ents, err := os.ReadDir(packs)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range ents {
		if strings.HasPrefix(ent.Name(), ".") {
			t.Fatal(ent.Name())
		}
	}
	list, err := svc.ListDatapacks()
	if err != nil {
		t.Fatal(err)
	}
	gotDesc := map[string]string{}
	for _, item := range list {
		gotDesc[item.Name] = item.Description
	}
	if gotDesc["quest"] != "Второй" || gotDesc["other"] != "Чужой" || len(list) != 2 {
		t.Fatalf("%+v", list)
	}
}

func datapackZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDatapackListWithoutWorldRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root")
	}
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=NewWorld\n")
	world := filepath.Join(root, "NewWorld")
	quest := filepath.Join(world, "datapacks", "quest")
	mustMkdir(t, quest)
	writeServerFile(t, filepath.Join(quest, "pack.mcmeta"), `{"pack":{"description":"Виден"}}`)
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(world, 0o755) })
	if err := os.Chmod(world, 0o111); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(world)
	if err != nil {
		if !errors.Is(err, os.ErrPermission) {
			t.Fatal(err)
		}
	} else {
		_, readErr := f.Readdirnames(1)
		f.Close()
		if readErr == nil {
			t.Fatal("world is listable")
		}
	}
	list, err := svc.ListDatapacks()
	if err != nil || errors.Is(err, ErrBackupDenied) {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "quest" || list[0].Description != "Виден" {
		t.Fatalf("%+v", list)
	}
	got, err := svc.InstallDatapack("extra.zip", bytes.NewReader(datapackZip(t, map[string]string{
		"pack.mcmeta": `{"pack":{"description":"Ещё"}}`,
	})))
	if err != nil || errors.Is(err, ErrBackupDenied) {
		t.Fatal(err)
	}
	if got.Name != "extra" || readServerFile(t, filepath.Join(world, "datapacks", "extra", "pack.mcmeta")) == "" {
		t.Fatal(got)
	}
}

func TestDatapackAccessNamesDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root")
	}
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=NewWorld\n")
	world := filepath.Join(root, "NewWorld")
	packs := filepath.Join(world, "datapacks")
	mustMkdir(t, packs)
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(packs, 0o755) })
	if err := os.Chmod(packs, 0); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ListDatapacks()
	if errors.Is(err, ErrBackupDenied) {
		t.Fatal(err)
	}
	var access *DatapackAccessError
	if !errors.As(err, &access) || !strings.Contains(err.Error(), packs) || !strings.Contains(err.Error(), "каталогу датапаков") {
		t.Fatal(err)
	}
}

func TestDatapackMissingPropertiesNotAccess(t *testing.T) {
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	quest := filepath.Join(root, "world", "datapacks", "quest")
	mustMkdir(t, quest)
	writeServerFile(t, filepath.Join(quest, "pack.mcmeta"), `{"pack":{"description":"Мир"}}`)
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListDatapacks()
	if err != nil || errors.Is(err, ErrBackupDenied) {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Description != "Мир" {
		t.Fatalf("%+v", list)
	}
}

func TestDatapackUnreadableProperties(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root")
	}
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	props := filepath.Join(root, "server.properties")
	writeServerFile(t, props, "level-name=NewWorld\n")
	mustMkdir(t, filepath.Join(root, "NewWorld"))
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(props, 0o644) })
	if err := os.Chmod(props, 0); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ListDatapacks()
	if errors.Is(err, ErrBackupDenied) || !errors.Is(err, ErrServerProperties) {
		t.Fatal(err)
	}
}

func TestDatapackBadLevelNotAccess(t *testing.T) {
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=..\n")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	_, err := svc.ListDatapacks()
	if errors.Is(err, ErrBackupDenied) || !errors.Is(err, ErrBackupCopy) {
		t.Fatal(err)
	}
}

func TestDatapackFileSave(t *testing.T) {
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=NewWorld\n")
	quest := filepath.Join(root, "NewWorld", "datapacks", "quest")
	mustMkdir(t, filepath.Join(quest, "data"))
	writeServerFile(t, filepath.Join(quest, "data", "foo.json"), "old")
	outside := filepath.Join(root, "NewWorld", "datapacks", "other.txt")
	writeServerFile(t, outside, "stay")
	bin := filepath.Join(quest, "data", "bin.txt")
	if err := os.WriteFile(bin, []byte{'a', 0, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveDatapackFile("quest", "data/foo.json", "new"); err != nil {
		t.Fatal(err)
	}
	if readServerFile(t, filepath.Join(quest, "data", "foo.json")) != "new" {
		t.Fatal("save")
	}
	err := svc.SaveDatapackFile("quest", "../other.txt", "hack")
	if !errors.Is(err, ErrDatapackPath) {
		t.Fatal(err)
	}
	err = svc.SaveDatapackFile("quest", "data/../../other.txt", "hack")
	if !errors.Is(err, ErrDatapackPath) {
		t.Fatal(err)
	}
	if readServerFile(t, outside) != "stay" {
		t.Fatal("outside")
	}
	if readServerFile(t, filepath.Join(quest, "data", "foo.json")) != "new" {
		t.Fatal("changed")
	}
	if err := svc.SaveDatapackFile("quest", "data/bin.txt", "text"); !errors.Is(err, ErrDatapackText) {
		t.Fatal(err)
	}
	body, err := os.ReadFile(bin)
	if err != nil || string(body) != "a\x00b" {
		t.Fatal(err, body)
	}
	files, err := svc.ListDatapackFiles("quest")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != "data/bin.txt" || files[1] != "data/foo.json" {
		t.Fatalf("%v", files)
	}
}
