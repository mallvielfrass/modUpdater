package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modupdater/internal/chunkbackup"
	"modupdater/internal/store"
)

func TestServerBackupAndRollback(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	if rec := e.do(http.MethodPost, "/api/admin/backups", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec := e.do(http.MethodGet, "/api/admin/backups", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatal(rec.Code)
	}
	rec := e.do(http.MethodPost, "/api/admin/backups", tok, "", nil)
	if errorCode(t, rec) != "mods_dir_unset" {
		t.Fatal(rec.Code, rec.Body.String())
	}

	e.setMods(t, tok)
	e.serverLayout(t)
	root := filepath.Dir(e.modsDir)
	writeText(t, filepath.Join(e.modsDir, "a.txt"), "v1")
	writeText(t, filepath.Join(root, "config", "c.txt"), "c1")
	writeText(t, filepath.Join(root, "world", "w.txt"), "w1")
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "crash-reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "saves", "New World"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(root, "logs", "latest.log"), "LOGDATA")
	writeText(t, filepath.Join(root, "crash-reports", "crash.txt"), "CRASH")
	writeText(t, filepath.Join(root, "saves", "New World", "level.dat"), "CLIENTSAVE")

	setBackupScope(t, e, tok, "full")
	rec = e.do(http.MethodPost, "/api/admin/backups/milestone", tok, "", nil)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var created struct {
		Backup store.Backup `json:"backup"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.Backup.Current || created.Backup.Label != "веха" || created.Backup.Kind != "milestone" || created.Backup.ID == "" || len(created.Backup.SHA256) != 64 {
		t.Fatalf("%+v", created.Backup)
	}
	snap := restoreChunk(t, e.data, created.Backup.ID)
	if readText(t, filepath.Join(snap, "mods", "a.txt")) != "v1" || readText(t, filepath.Join(snap, "world", "w.txt")) != "w1" {
		t.Fatal("snapshot files")
	}
	if backupHas(t, snap, "LOGDATA") || backupHas(t, snap, "CRASH") || backupHas(t, snap, "CLIENTSAVE") {
		t.Fatal("excluded files")
	}

	writeText(t, filepath.Join(e.modsDir, "a.txt"), "v2")
	writeText(t, filepath.Join(root, "config", "c.txt"), "c2")
	writeText(t, filepath.Join(root, "world", "w.txt"), "w2")
	rec = e.do(http.MethodPost, "/api/admin/backups/nope/rollback", tok, "", nil)
	if errorCode(t, rec) != "not_found" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodPost, "/api/admin/backups/"+created.Backup.ID+"/rollback", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var rolled struct {
		Backups        []store.Backup         `json:"backups"`
		PendingRestart []store.PendingRestart `json:"pending_restart"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rolled); err != nil {
		t.Fatal(err)
	}
	if readText(t, filepath.Join(e.modsDir, "a.txt")) != "v1" || readText(t, filepath.Join(root, "config", "c.txt")) != "c1" || readText(t, filepath.Join(root, "world", "w.txt")) != "w1" {
		t.Fatal("live files")
	}
	if len(rolled.Backups) != 2 {
		t.Fatal(len(rolled.Backups))
	}
	sawCurrent := false
	sawPrev := false
	for _, b := range rolled.Backups {
		if b.ID == created.Backup.ID {
			if !b.Current {
				t.Fatal("current")
			}
			sawCurrent = true
		} else if b.Current {
			t.Fatal("two current")
		} else if readText(t, filepath.Join(restoreChunk(t, e.data, b.ID), "mods", "a.txt")) == "v2" {
			sawPrev = true
		}
	}
	if !sawCurrent || !sawPrev {
		t.Fatal(sawCurrent, sawPrev)
	}
	named := false
	for _, item := range rolled.PendingRestart {
		if item.Name == "откат" {
			named = true
		}
	}
	if !named {
		t.Fatalf("%+v", rolled.PendingRestart)
	}
	hist := e.do(http.MethodGet, "/api/history", "", "", nil)
	if strings.Contains(hist.Body.String(), "откат") || strings.Contains(hist.Body.String(), "server-rollback") {
		t.Fatal(hist.Body.String())
	}

	restarted := false
	e.mc.Run = func(name string, args []string) error {
		restarted = name == "systemctl" && len(args) == 2 && args[0] == "restart"
		return nil
	}
	rec = e.do(http.MethodPost, "/api/admin/server/restart", tok, "application/json", strings.NewReader(`{}`))
	if rec.Code != http.StatusOK || !restarted {
		t.Fatal(rec.Code, rec.Body.String(), restarted)
	}
	rec = e.do(http.MethodGet, "/api/admin/backups", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var listed struct {
		Backups []store.Backup `json:"backups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Backups) != 3 {
		t.Fatal(len(listed.Backups))
	}
	auto := 0
	for _, b := range listed.Backups {
		if b.Label == "перед перезапуском" && b.Current {
			auto++
		}
	}
	if auto != 1 {
		t.Fatalf("%+v", listed.Backups)
	}
	if !strings.Contains(rec.Body.String(), `"progress"`) || !strings.Contains(rec.Body.String(), `"running":false`) {
		t.Fatal(rec.Body.String())
	}

	if err := os.RemoveAll(filepath.Join(root, "config")); err != nil {
		t.Fatal(err)
	}
	again := false
	e.mc.Run = func(string, []string) error {
		again = true
		return nil
	}
	rec = e.do(http.MethodPost, "/api/admin/server/restart", tok, "application/json", strings.NewReader(`{}`))
	if rec.Code != http.StatusOK || !again {
		t.Fatal(rec.Code, rec.Body.String(), again)
	}
}

func TestDailyBackupTime(t *testing.T) {
	e := newEnv(t)
	body := strings.NewReader(`{"time":"03:05"}`)
	if rec := e.do(http.MethodPut, "/api/admin/backups/time", "", "application/json", body); rec.Code != http.StatusUnauthorized {
		t.Fatal(rec.Code, rec.Body.String())
	}
	tok := e.token(t)
	rec := e.do(http.MethodPut, "/api/admin/backups/time", tok, "application/json", strings.NewReader(`{"time":"25:99"}`))
	if errorCode(t, rec) != "Некорректное время бекапа." {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodPut, "/api/admin/backups/time", tok, "application/json", strings.NewReader(`{"time":"03:05"}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"time":"03:05"`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodGet, "/api/admin/backups", tok, "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"daily_time":"03:05"`) {
		t.Fatal(rec.Body.String())
	}
	rec = e.do(http.MethodPut, "/api/admin/backups/time", tok, "application/json", strings.NewReader(`{"time":""}`))
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	rec = e.do(http.MethodGet, "/api/admin/backups", tok, "", nil)
	if !strings.Contains(rec.Body.String(), `"daily_time":""`) {
		t.Fatal(rec.Body.String())
	}
}

func TestBackupScopeSetting(t *testing.T) {
	e := newEnv(t)
	if rec := e.do(http.MethodPut, "/api/admin/backups/scope", "", "application/json", strings.NewReader(`{"scope":"full"}`)); rec.Code != http.StatusUnauthorized {
		t.Fatal(rec.Code, rec.Body.String())
	}
	tok := e.token(t)
	rec := e.do(http.MethodGet, "/api/admin/backups", tok, "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"backup_scope":"world"`) {
		t.Fatal(rec.Body.String())
	}
	rec = e.do(http.MethodPut, "/api/admin/backups/scope", tok, "application/json", strings.NewReader(`{"scope":"nope"}`))
	if errorCode(t, rec) != "Некорректная область бекапа." {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodPut, "/api/admin/backups/scope", tok, "application/json", strings.NewReader(`{"scope":"full"}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"scope":"full"`) {
		t.Fatal(rec.Body.String())
	}
	rec = e.do(http.MethodGet, "/api/admin/backups", tok, "", nil)
	if !strings.Contains(rec.Body.String(), `"backup_scope":"full"`) {
		t.Fatal(rec.Body.String())
	}
	rec = e.do(http.MethodPut, "/api/admin/backups/scope", tok, "application/json", strings.NewReader(`{"scope":""}`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"scope":"world"`) {
		t.Fatal(rec.Body.String())
	}

	e.setMods(t, tok)
	e.serverLayout(t)
	root := filepath.Dir(e.modsDir)
	writeText(t, filepath.Join(e.modsDir, "a.txt"), "mod")
	writeText(t, filepath.Join(root, "config", "c.txt"), "cfg")
	writeText(t, filepath.Join(root, "world", "w.txt"), "map")
	rec = e.do(http.MethodPost, "/api/admin/backups/milestone", tok, "", nil)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var created struct {
		Backup store.Backup `json:"backup"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	snap := restoreChunk(t, e.data, created.Backup.ID)
	if readText(t, filepath.Join(snap, "world", "w.txt")) != "map" {
		t.Fatal("world")
	}
	if _, err := os.Stat(filepath.Join(snap, "mods")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(snap, "config")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestDeleteBackupUnknown(t *testing.T) {
	e := newEnv(t)
	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if rec := e.do(http.MethodDelete, "/api/admin/backups/"+id, "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatal(rec.Code, rec.Body.String())
	}
	tok := e.token(t)
	rec := e.do(http.MethodDelete, "/api/admin/backups/nope", tok, "", nil)
	if !strings.Contains(rec.Body.String(), "Бекап не найден.") {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func restoreChunk(t *testing.T, data, id string) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "tree-"+id)
	if err := chunkbackup.RestoreStored(filepath.Join(data, "backups"), id, dest); err != nil {
		t.Fatal(err)
	}
	return dest
}

func setBackupScope(t *testing.T, e *env, tok, scope string) {
	t.Helper()
	rec := e.do(http.MethodPut, "/api/admin/backups/scope", tok, "application/json", strings.NewReader(`{"scope":"`+scope+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func writeText(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func backupHas(t *testing.T, root, needle string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), needle) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}
