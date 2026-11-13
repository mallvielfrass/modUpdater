package httpapi

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modupdater/internal/auth"
	"modupdater/internal/config"
	"modupdater/internal/minecraft"
	"modupdater/internal/mods"
	"modupdater/internal/store"
)

type env struct {
	api     *API
	db      *store.DB
	data    string
	modsDir string
	mc      *minecraft.Service
}

func newEnv(t *testing.T) *env {
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
	au, err := auth.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := au.BootstrapRoot("admin", "adminpass1"); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Listen:    "127.0.0.1:0",
		DataDir:   data,
		PublicURL: "http://127.0.0.1:8080",
		Minecraft: config.Minecraft{
			SystemdUnit:     "minecraft.service",
			RestartWarning:  "Перезапуск сервера через 15 секунд: обновление модов",
			RestartDelaySec: 0,
			RCON:            config.RCON{Host: "127.0.0.1", Port: 25575, Password: "rcon-secret"},
		},
	}
	mc := minecraft.New(cfg.Minecraft.SystemdUnit, cfg.Minecraft.RestartWarning, 0, "127.0.0.1", 25575, "rcon-secret")
	mc.Say = func(string) error { return nil }
	mc.Exec = func(string) (string, error) { return "", errors.New("rcon") }
	mc.Run = func(string, []string) error { t.Fatal("systemctl"); return nil }
	mc.Output = func(name string, args []string) (string, error) {
		if name == "systemctl" {
			return "", errors.New("unavailable")
		}
		t.Fatal(name)
		return "", nil
	}
	return &env{api: New(cfg, au, mods.New(db, data), mc), db: db, data: data, modsDir: modsDir, mc: mc}
}

func (e *env) do(method, path, token, ctype string, body io.Reader) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, body)
	req.RemoteAddr = "192.0.2.60:1"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	e.api.ServeHTTP(rec, req)
	return rec
}

func (e *env) token(t *testing.T) string {
	t.Helper()
	rec := e.do(http.MethodPost, "/api/auth/login", "", "application/json", strings.NewReader(`{"username":"admin","password":"adminpass1"}`))
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Token == "" {
		t.Fatal(rec.Body.String())
	}
	return body.Token
}

func (e *env) serverLayout(t *testing.T) {
	t.Helper()
	root := filepath.Dir(e.modsDir)
	for _, name := range []string{"config", "world"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "server.properties"), []byte("level-name=world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e *env) setMods(t *testing.T, tok string) {
	t.Helper()
	rec := e.do(http.MethodPut, "/api/admin/settings", tok, "application/json", strings.NewReader(`{"mods_dir":`+jsonString(e.modsDir)+`}`))
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func jarBytes(t *testing.T, id, version, name, desc string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("fabric.mod.json")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"id": id, "version": version, "name": name, "description": desc})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func multipartBody(t *testing.T, filename string, content []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if filename != "" {
		fw, err := w.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(rec.Body.String())
	}
	return body.Error
}

func TestPublicHiddenAndUnknownSameNotFound(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	body, ctype := multipartBody(t, "Hidden.jar", jarBytes(t, "hidden", "1", "Hidden", "no"), map[string]string{"client": "false"})
	rec := e.do(http.MethodPost, "/api/admin/mods", tok, ctype, body)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var created struct {
		Mod store.Mod `json:"mod"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	hidden := e.do(http.MethodGet, "/api/mods/"+created.Mod.ID+"/file", "", "", nil)
	unknown := e.do(http.MethodGet, "/api/mods/0123456789abcdef0123456789abcdef/file", "", "", nil)
	if hidden.Code != unknown.Code || hidden.Body.String() != unknown.Body.String() {
		t.Fatalf("%d %s vs %d %s", hidden.Code, hidden.Body.String(), unknown.Code, unknown.Body.String())
	}
	if errorCode(t, hidden) != "not_found" {
		t.Fatal(hidden.Body.String())
	}
	one := e.do(http.MethodGet, "/api/mods/"+created.Mod.ID, "", "", nil)
	if errorCode(t, one) != "not_found" {
		t.Fatal(one.Body.String())
	}
}

func TestHistoryHiddenWhenLiveClientFalse(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	body, ctype := multipartBody(t, "Live.jar", jarBytes(t, "live", "1", "Live", "d"), map[string]string{"client": "true"})
	rec := e.do(http.MethodPost, "/api/admin/mods", tok, ctype, body)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var created struct {
		Mod store.Mod `json:"mod"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	body, ctype = multipartBody(t, "Live.jar", jarBytes(t, "live", "2", "Live", "d2"), nil)
	rec = e.do(http.MethodPut, "/api/admin/mods/"+created.Mod.ID, tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	hist := e.do(http.MethodGet, "/api/history", "", "", nil)
	var events struct {
		Events []mods.Event `json:"events"`
	}
	if err := json.Unmarshal(hist.Body.Bytes(), &events); err != nil {
		t.Fatal(hist.Body.String())
	}
	var id string
	for _, ev := range events.Events {
		if ev.HasFile {
			id = ev.ID
		}
	}
	if id == "" {
		t.Fatal(hist.Body.String())
	}
	body, ctype = multipartBody(t, "", nil, map[string]string{"client": "false"})
	rec = e.do(http.MethodPut, "/api/admin/mods/"+created.Mod.ID, tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	file := e.do(http.MethodGet, "/api/history/"+id+"/file", "", "", nil)
	if file.Code != http.StatusNotFound || errorCode(t, file) != "not_found" {
		t.Fatal(file.Code, file.Body.String())
	}
	hist = e.do(http.MethodGet, "/api/history", "", "", nil)
	if strings.Contains(hist.Body.String(), id) {
		t.Fatal(hist.Body.String())
	}
}

func TestDeletedClientHistoryDownloadable(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	content := jarBytes(t, "keep", "1", "Keep", "d")
	body, ctype := multipartBody(t, "Keep.jar", content, map[string]string{"client": "true"})
	rec := e.do(http.MethodPost, "/api/admin/mods", tok, ctype, body)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var created struct {
		Mod store.Mod `json:"mod"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	rec = e.do(http.MethodDelete, "/api/admin/mods/"+created.Mod.ID, tok, "", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code, rec.Body.String())
	}
	hist := e.do(http.MethodGet, "/api/history", "", "", nil)
	var events struct {
		Events []mods.Event `json:"events"`
	}
	if err := json.Unmarshal(hist.Body.Bytes(), &events); err != nil {
		t.Fatal(hist.Body.String())
	}
	var id string
	for _, ev := range events.Events {
		if ev.Action == "deleted" && ev.HasFile {
			id = ev.ID
		}
	}
	if id == "" {
		t.Fatal(hist.Body.String())
	}
	file := e.do(http.MethodGet, "/api/history/"+id+"/file", "", "", nil)
	if file.Code != http.StatusOK {
		t.Fatal(file.Code, file.Body.String())
	}
	if file.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatal(file.Header().Get("Content-Type"))
	}
	if file.Header().Get("Content-Disposition") != `attachment; filename="Keep.jar"` {
		t.Fatal(file.Header().Get("Content-Disposition"))
	}
	if !bytes.Equal(file.Body.Bytes(), content) {
		sum := sha256.Sum256(file.Body.Bytes())
		t.Fatalf("bytes differ %s", hex.EncodeToString(sum[:]))
	}
}

func TestAddSlugExists(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	body, ctype := multipartBody(t, "A.jar", jarBytes(t, "alpha", "1", "A", "d"), nil)
	rec := e.do(http.MethodPost, "/api/admin/mods", tok, ctype, body)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	body, ctype = multipartBody(t, "B.jar", jarBytes(t, "alpha", "2", "B", "d"), nil)
	rec = e.do(http.MethodPost, "/api/admin/mods", tok, ctype, body)
	if errorCode(t, rec) != "slug_exists" {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestAddRefusesUntrackedJar(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	content := jarBytes(t, "plain", "1", "P", "d")
	path := filepath.Join(e.modsDir, "Plain.jar")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body, ctype := multipartBody(t, "Plain.jar", jarBytes(t, "other", "1", "O", "d"), nil)
	rec := e.do(http.MethodPost, "/api/admin/mods", tok, ctype, body)
	if errorCode(t, rec) != "conflict" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("untracked jar replaced", err)
	}
}
