package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "modupdater.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func userVersion(t *testing.T, db *DB) int {
	t.Helper()
	var v int
	if err := db.sql.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMigrateFresh(t *testing.T) {
	db := openTestDB(t)
	if userVersion(t, db) != 14 {
		t.Fatal(userVersion(t, db))
	}
	var n int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('mod_configs', 'client_releases', 'refresh_tokens', 'backups')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatal(n)
	}
	last, err := db.LastRestartAt()
	if err != nil || last != "" {
		t.Fatal(last, err)
	}
	active, err := db.ActiveClientVersion()
	if err != nil || active != "" {
		t.Fatal(active, err)
	}
}

func TestMigrateLegacyV0(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "modupdater.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := execScript(raw, migration001); err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(
		`INSERT INTO mods (id, slug, name, description, filename, sha256, size, version, client, updated_at)
		 VALUES ('0123456789abcdef0123456789abcdef', 'oldmod', 'Old', '', 'old.jar', 'abc', 3, '1', 1, '2020-01-01T00:00:00Z')`,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(
		`INSERT INTO history (id, mod_id, slug, name, action, filename, version, sha256, size, client, created_at)
		 VALUES ('abcdef0123456789abcdef0123456789', '0123456789abcdef0123456789abcdef', 'oldmod', 'Old', 'added', 'old.jar', '1', 'abc', 3, 1, '2020-01-01T00:00:00Z')`,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(
		`INSERT INTO history (id, mod_id, slug, name, action, filename, version, sha256, size, client, created_at)
		 VALUES ('abcdef0123456789abcdef0123456790', '0123456789abcdef0123456789abcdef', 'oldmod', 'Old', 'updated', 'old.jar', '1', 'abc', 3, 1, '2019-01-01T00:00:00Z')`,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(
		`INSERT INTO history (id, mod_id, slug, name, action, filename, version, sha256, size, client, created_at)
		 VALUES ('abcdef0123456789abcdef0123456791', '0123456789abcdef0123456789abcdef', 'oldmod', 'Old', 'added', 'old.jar', '1', 'abc', 3, 1, '2024-01-01T00:00:00Z')`,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(
		`INSERT INTO mods (id, slug, name, description, filename, sha256, size, version, client, updated_at)
		 VALUES ('fedcba9876543210fedcba9876543210', 'nohist', 'NoHist', '', 'nohist.jar', 'def', 1, '1', 1, '2021-05-06T07:08:09Z')`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if userVersion(t, db) != 14 {
		t.Fatal(userVersion(t, db))
	}
	m, err := db.GetMod("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if m.Slug != "oldmod" || m.ClientOnly || !m.Client || m.Size != 3 || m.CreatedAt != "2020-01-01T00:00:00Z" {
		t.Fatalf("%+v", m)
	}
	nh, err := db.GetMod("fedcba9876543210fedcba9876543210")
	if err != nil || nh.CreatedAt != "2021-05-06T07:08:09Z" {
		t.Fatal(err, nh.CreatedAt)
	}
	pending, err := db.PendingRestart("")
	if err != nil || len(pending) != 1 || pending[0].Slug != "oldmod" || pending[0].Action != "added" {
		t.Fatal(err, pending)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if userVersion(t, db) != 14 {
		t.Fatal(userVersion(t, db))
	}
	if _, err := db.GetMod("0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
}

func TestPendingGroupsHistory(t *testing.T) {
	db := openTestDB(t)
	rows := []struct {
		id, slug, name, action, at string
		clientOnly                 int
	}{
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", "alpha", "A1", "added", "2020-01-01T00:00:00Z", 0},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2", "alpha", "A2", "updated", "2020-01-01T00:00:01Z", 0},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa3", "alpha", "A3", "deleted", "2020-01-01T00:00:02Z", 0},
		{"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb1", "beta", "B", "added", "2020-01-01T00:00:01Z", 0},
		{"ccccccccccccccccccccccccccccccc1", "gamma", "G", "added", "2020-01-01T00:00:02Z", 1},
	}
	for _, row := range rows {
		_, err := db.sql.Exec(
			`INSERT INTO history (id, mod_id, slug, name, action, filename, version, sha256, size, client, client_only, created_at)
			 VALUES (?, ?, ?, ?, ?, 'f.jar', '1', 'abc', 1, 1, ?, ?)`,
			row.id, row.id, row.slug, row.name, row.action, row.clientOnly, row.at,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	pending, err := db.PendingRestart("")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || pending[0].Slug != "beta" || pending[0].Action != "added" || pending[0].Name != "B" {
		t.Fatalf("%+v", pending)
	}
	if pending[1].Slug != "alpha" || pending[1].Action != "deleted" || pending[1].Name != "A3" || pending[1].Filename != "f.jar" {
		t.Fatalf("%+v", pending)
	}
	pending, err = db.PendingRestart("2020-01-01T00:00:01Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Slug != "alpha" || pending[0].Action != "deleted" {
		t.Fatalf("%+v", pending)
	}
}

func TestListModsOrderAndCreatedAt(t *testing.T) {
	db := openTestDB(t)
	insert := func(id, slug, name, filename, created string) {
		t.Helper()
		m := Mod{
			ID: id, Slug: slug, Name: name, Filename: filename, SHA256: "abc", Size: 1,
			Version: "1", Client: true, UpdatedAt: created, CreatedAt: created,
		}
		h := History{
			ID: id, ModID: id, Slug: slug, Name: name, Action: "added", Filename: filename,
			Version: "1", SHA256: "abc", Size: 1, Client: true, CreatedAt: created,
		}
		if err := db.InsertModWithHistory(m, h); err != nil {
			t.Fatal(err)
		}
	}
	insert("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", "b", "beta", "b.jar", "2020-01-02T00:00:00Z")
	insert("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2", "a", "Alpha", "z.jar", "2020-01-03T00:00:00Z")
	insert("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa3", "c", "alpha", "a.jar", "2020-01-04T00:00:00Z")
	list, err := db.ListMods(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Filename != "a.jar" || list[1].Filename != "z.jar" || list[2].Filename != "b.jar" {
		t.Fatalf("%s %s %s", list[0].Name, list[1].Name, list[2].Name)
	}
	if err := db.UpdateModMeta(list[0].ID, "ALPHA", "d", true, false, "2021-02-03T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateModFile(Mod{
		ID: list[0].ID, Filename: "a2.jar", SHA256: "def", Size: 2, Version: "2",
		Name: "ALPHA", Description: "d", Client: true, UpdatedAt: "2022-01-01T00:00:00Z", CreatedAt: "2099-01-01T00:00:00Z",
	}, History{
		ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa4", ModID: list[0].ID, Slug: "c", Name: "ALPHA", Action: "updated",
		Filename: "a2.jar", Version: "2", SHA256: "def", Size: 2, Client: true, CreatedAt: "2022-01-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetMod(list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CreatedAt != "2020-01-04T00:00:00Z" || got.UpdatedAt != "2022-01-01T00:00:00Z" || got.Filename != "a2.jar" {
		t.Fatalf("%+v", got)
	}
}
