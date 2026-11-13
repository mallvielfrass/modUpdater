package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"modupdater/internal/store"
)

func TestServiceStartClearsPending(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	body, ctype := multipartBody(t, "Srv.jar", jarBytes(t, "srv", "1", "Srv", "d"), nil)
	rec := e.do(http.MethodPost, "/api/admin/mods", tok, ctype, body)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var created struct {
		Mod modJSON `json:"mod"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	body, ctype = multipartBody(t, "Only.jar", jarBytes(t, "only", "1", "Only", "d"), map[string]string{"client_only": "true"})
	rec = e.do(http.MethodPost, "/api/admin/mods", tok, ctype, body)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	admin := e.adminMods(t, tok)
	if len(admin.PendingRestart) != 1 || admin.PendingRestart[0].Slug != "srv" || admin.PendingRestart[0].Action != "added" {
		t.Fatalf("%+v", admin.PendingRestart)
	}
	addedAt := admin.PendingRestart[0].CreatedAt
	body, ctype = multipartBody(t, "", nil, map[string]string{"name": "Renamed"})
	rec = e.do(http.MethodPut, "/api/admin/mods/"+created.Mod.ID, tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	admin = e.adminMods(t, tok)
	if len(admin.PendingRestart) != 1 || admin.PendingRestart[0].Action != "added" || admin.PendingRestart[0].CreatedAt != addedAt || admin.PendingRestart[0].Name != "Srv" {
		t.Fatalf("%+v", admin.PendingRestart)
	}

	prev, err := time.Parse(time.RFC3339, addedAt)
	if err != nil {
		t.Fatal(err)
	}
	started := prev.Add(time.Second)
	e.mc.Output = func(name string, args []string) (string, error) {
		if name != "systemctl" || len(args) < 2 || args[0] != "show" || args[1] != "minecraft.service" {
			t.Fatalf("%s %#v", name, args)
		}
		return strconv.FormatInt(started.Unix(), 10), nil
	}
	admin = e.adminMods(t, tok)
	if admin.LastRestartAt != store.FormatTime(started) || len(admin.PendingRestart) != 0 {
		t.Fatalf("%s %+v", admin.LastRestartAt, admin.PendingRestart)
	}

	earlier := prev.Add(-time.Hour)
	e.mc.Output = func(name string, args []string) (string, error) {
		if name != "systemctl" {
			t.Fatal(name)
		}
		return strconv.FormatInt(earlier.Unix(), 10), nil
	}
	admin = e.adminMods(t, tok)
	if admin.LastRestartAt != store.FormatTime(started) || len(admin.PendingRestart) != 0 {
		t.Fatalf("%s %+v", admin.LastRestartAt, admin.PendingRestart)
	}

	e.mc.Output = func(string, []string) (string, error) { return "", errors.New("unavailable") }
	if wait := time.Until(started.Add(time.Second)); wait > 0 {
		time.Sleep(wait + 20*time.Millisecond)
	}
	body, ctype = multipartBody(t, "Srv.jar", jarBytes(t, "srv", "2", "Srv", "d2"), nil)
	rec = e.do(http.MethodPut, "/api/admin/mods/"+created.Mod.ID, tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	admin = e.adminMods(t, tok)
	if len(admin.PendingRestart) != 1 || admin.PendingRestart[0].Action != "updated" || admin.PendingRestart[0].Slug != "srv" {
		t.Fatalf("%s %+v", admin.LastRestartAt, admin.PendingRestart)
	}

	e.serverLayout(t)
	rec = e.do(http.MethodPost, "/api/admin/backups/milestone", tok, "", nil)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var saved struct {
		Backup store.Backup `json:"backup"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	rec = e.do(http.MethodPost, "/api/admin/backups/"+saved.Backup.ID+"/rollback", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var rolled struct {
		PendingRestart []store.PendingRestart `json:"pending_restart"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rolled); err != nil {
		t.Fatal(err)
	}
	sawRollback := false
	for _, item := range rolled.PendingRestart {
		if item.Name == "откат" {
			sawRollback = true
		}
		if item.Slug == "only" {
			t.Fatal(item)
		}
	}
	if !sawRollback {
		t.Fatalf("%+v", rolled.PendingRestart)
	}
	future := time.Now().UTC().Add(3 * time.Second).Truncate(time.Second)
	e.mc.Output = func(name string, args []string) (string, error) {
		if name != "systemctl" || !strings.Contains(strings.Join(args, " "), "minecraft.service") {
			t.Fatalf("%s %#v", name, args)
		}
		return "@" + strconv.FormatInt(future.Unix(), 10), nil
	}
	admin = e.adminMods(t, tok)
	if admin.LastRestartAt != store.FormatTime(future) || len(admin.PendingRestart) != 0 {
		t.Fatalf("%s %+v", admin.LastRestartAt, admin.PendingRestart)
	}
}
