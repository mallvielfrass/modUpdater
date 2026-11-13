package mods

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modupdater/internal/store"
)

func TestChangelogFromHistoryFilesAndMilestone(t *testing.T) {
	db, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeJar(t, filepath.Join(modsDir, "alpha.jar"), jarBytes(t, "alpha", "1.0.0", "Alpha", "d"))
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	oldPlugin := filepath.Join(root, "plugins", "old.jar")
	if err := os.MkdirAll(filepath.Dir(oldPlugin), 0o755); err != nil {
		t.Fatal(err)
	}
	writeJar(t, oldPlugin, []byte("old"))
	yesterday := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPlugin, yesterday, yesterday); err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(root, "modpacks", "sky")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPack := filepath.Join(root, "world", "datapacks", "oldpack")
	if err := os.MkdirAll(oldPack, 0o755); err != nil {
		t.Fatal(err)
	}
	first, err := svc.GenerateChangelogs()
	if err != nil {
		t.Fatal(err)
	}
	if !hasLine(first, "добавлен мод Alpha") || hasLine(first, "плагин") || hasLine(first, "модпак") || hasLine(first, "датапак") {
		t.Fatalf("%+v", first)
	}
	writeJar(t, filepath.Join(root, "plugins", "new.jar"), []byte("new"))
	if err := os.Remove(oldPlugin); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(pack); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(oldPack); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "world", "datapacks", "newpack"), 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := store.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AddBackup(store.Backup{
		ID: id, CreatedAt: store.Now(), Label: "веха", Kind: "milestone", SHA256: strings.Repeat("a", 64),
	}); err != nil {
		t.Fatal(err)
	}
	second, err := svc.GenerateChangelogs()
	if err != nil {
		t.Fatal(err)
	}
	if !hasLine(second, "добавлен мод Alpha") || !hasLine(second, "добавлен плагин new.jar") || !hasLine(second, "удалён плагин old.jar") || !hasLine(second, "удалён модпак sky") || !hasLine(second, "добавлен датапак newpack") || !hasLine(second, "удалён датапак oldpack") || !hasLine(second, "сделан бэкап вехи") {
		t.Fatalf("%+v", second)
	}
	again, err := svc.GenerateChangelogs()
	if err != nil {
		t.Fatal(err)
	}
	if lines(again) != lines(second) {
		t.Fatalf("again %+v second %+v", again, second)
	}
}

func hasLine(days []ChangelogDay, part string) bool {
	for _, day := range days {
		for _, line := range day.Lines {
			if strings.Contains(line, part) {
				return true
			}
		}
	}
	return false
}

func lines(days []ChangelogDay) int {
	n := 0
	for _, day := range days {
		n += len(day.Lines)
	}
	return n
}
