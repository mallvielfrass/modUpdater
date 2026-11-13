package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAdminChangelog(t *testing.T) {
	e := newEnv(t)
	if rec := e.do(http.MethodGet, "/api/admin/changelog", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatal(rec.Code)
	}
	tok := e.token(t)
	e.setMods(t, tok)
	rec := e.do(http.MethodPost, "/api/admin/changelog", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var body struct {
		Days []struct {
			Day   string   `json:"day"`
			Lines []string `json:"lines"`
		} `json:"days"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Days == nil {
		t.Fatal("nil days")
	}
	rec = e.do(http.MethodGet, "/api/admin/changelog", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
}

func TestChangelogPage(t *testing.T) {
	e := newEnv(t)
	page := e.do(http.MethodGet, "/changelog", "", "", nil)
	if page.Code != http.StatusOK || page.Header().Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(page.Body.String(), "Нет изменений") {
		t.Fatal(page.Code, page.Body.String())
	}
	tok := e.token(t)
	e.setMods(t, tok)
	body, ctype := multipartBody(t, "LogMod.jar", jarBytes(t, "logmod", "1", "LogMod", "d"), map[string]string{"client": "false"})
	rec := e.do(http.MethodPost, "/api/admin/mods", tok, ctype, body)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	rec = e.do(http.MethodPost, "/api/admin/changelog", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	page = e.do(http.MethodGet, "/changelog", "", "", nil)
	html := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(html, "добавлен мод LogMod") || !strings.Contains(html, "<li>") {
		t.Fatal(html)
	}
	home := e.do(http.MethodGet, "/", "", "", nil)
	if !strings.Contains(home.Body.String(), `href="/changelog"`) {
		t.Fatal(home.Body.String())
	}
}
