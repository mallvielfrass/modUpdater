package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"modupdater/internal/auth"
	"modupdater/internal/config"
	"modupdater/internal/httpapi"
	"modupdater/internal/minecraft"
	"modupdater/internal/mods"
	"modupdater/internal/store"
)

func testStack(t *testing.T, password string) (*store.DB, *auth.Service, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "modupdater.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	au, err := auth.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := au.BootstrapRoot("admin", password); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Listen:    "127.0.0.1:0",
		DataDir:   dir,
		PublicURL: "http://127.0.0.1:8080",
		RootAdmin: config.RootAdmin{Username: "admin", Password: password},
		Minecraft: config.Minecraft{
			SystemdUnit:     "minecraft.service",
			RestartWarning:  "Перезапуск сервера через 15 секунд: обновление модов",
			RestartDelaySec: 0,
			RCON:            config.RCON{Host: "127.0.0.1", Port: 25575, Password: "rcon-secret"},
		},
	}
	mc := minecraft.New(cfg.Minecraft.SystemdUnit, cfg.Minecraft.RestartWarning, 0, cfg.Minecraft.RCON.Host, cfg.Minecraft.RCON.Port, cfg.Minecraft.RCON.Password)
	mc.Say = func(string) error { return nil }
	mc.Run = func(string, []string) error { return nil }
	mc.Output = func(string, []string) (string, error) { return "", nil }
	h := httpapi.New(cfg, au, mods.New(db, dir), mc)
	return db, au, h
}

func do(h http.Handler, method, path, addr string, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = addr
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

func TestSecondStartKeepsRootHash(t *testing.T) {
	db, au, _ := testStack(t, "")
	hash1, err := db.PasswordHash("admin")
	if err != nil {
		t.Fatal(err)
	}
	cost, err := bcrypt.Cost([]byte(hash1))
	if err != nil || cost != 12 {
		t.Fatalf("cost %d %v", cost, err)
	}
	generated, err := au.BootstrapRoot("admin", "other-password")
	if err != nil {
		t.Fatal(err)
	}
	if generated != "" {
		t.Fatal("password reset")
	}
	hash2, err := db.PasswordHash("admin")
	if err != nil {
		t.Fatal(err)
	}
	if hash1 != hash2 {
		t.Fatal("root hash changed")
	}
}

func TestLoginUnknownAndBadPasswordSameBody(t *testing.T) {
	_, _, h := testStack(t, "adminpass1")
	unknown := do(h, http.MethodPost, "/api/auth/login", "192.0.2.1:1", `{"username":"nosuch","password":"password1"}`)
	bad := do(h, http.MethodPost, "/api/auth/login", "192.0.2.2:1", `{"username":"admin","password":"wrongpass"}`)
	if unknown.Code != http.StatusUnauthorized || bad.Code != http.StatusUnauthorized {
		t.Fatalf("%d %d", unknown.Code, bad.Code)
	}
	if unknown.Body.String() != bad.Body.String() {
		t.Fatalf("%q vs %q", unknown.Body.String(), bad.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(unknown.Body.Bytes(), &body); err != nil || body.Error != "unauthorized" {
		t.Fatal(unknown.Body.String())
	}
	if unknown.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal(unknown.Header())
	}
	if unknown.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("credentials header set")
	}
}

func TestEleventhLoginRateLimited(t *testing.T) {
	_, _, h := testStack(t, "adminpass1")
	addr := "192.0.2.8:9"
	for i := 0; i < 10; i++ {
		rec := do(h, http.MethodPost, "/api/auth/login", addr, `{"username":"nosuch","password":"password1"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	rec := do(h, http.MethodPost, "/api/auth/login", addr, `{"username":"nosuch","password":"password1"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error != "rate_limited" {
		t.Fatal(rec.Body.String())
	}
}

func TestSuccessClearsRateLimit(t *testing.T) {
	_, _, h := testStack(t, "adminpass1")
	addr := "192.0.2.9:9"
	for i := 0; i < 9; i++ {
		rec := do(h, http.MethodPost, "/api/auth/login", addr, `{"username":"nosuch","password":"password1"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Fatal(rec.Body.String())
		}
	}
	ok := do(h, http.MethodPost, "/api/auth/login", addr, `{"username":"admin","password":"adminpass1"}`)
	if ok.Code != http.StatusOK {
		t.Fatal(ok.Body.String())
	}
	for i := 0; i < 2; i++ {
		rec := do(h, http.MethodPost, "/api/auth/login", addr, `{"username":"nosuch","password":"password1"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("after success %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
}

func TestLogoutMeAndUnknownPath(t *testing.T) {
	_, _, h := testStack(t, "adminpass1")
	ok := do(h, http.MethodPost, "/api/auth/login", "192.0.2.3:1", `{"username":"admin","password":"adminpass1"}`)
	if ok.Code != http.StatusOK {
		t.Fatal(ok.Body.String())
	}
	var login struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(ok.Body.Bytes(), &login); err != nil || login.Token == "" {
		t.Fatal(ok.Body.String())
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+login.Token)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "password") {
		t.Fatal(rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+login.Token)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"error":"not_found"`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Methods") != "GET, POST, PUT, DELETE, OPTIONS" {
		t.Fatal(rec.Header().Get("Access-Control-Allow-Methods"))
	}
}

func TestRefreshToken(t *testing.T) {
	db, _, h := testStack(t, "adminpass1")
	ok := do(h, http.MethodPost, "/api/auth/login", "192.0.2.9:1", `{"username":"admin","password":"adminpass1"}`)
	if ok.Code != http.StatusOK {
		t.Fatal(ok.Body.String())
	}
	var login struct {
		Token        string `json:"token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    string `json:"expires_at"`
		Admin        struct {
			ID string `json:"id"`
		} `json:"admin"`
	}
	if err := json.Unmarshal(ok.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	if !store.ValidToken(login.Token) || !store.ValidToken(login.RefreshToken) || login.ExpiresAt == "" || login.Admin.ID == "" {
		t.Fatal(ok.Body.String())
	}
	if strings.Contains(ok.Body.String(), "adminpass1") || strings.Contains(ok.Body.String(), "password_hash") {
		t.Fatal(ok.Body.String())
	}
	rec := do(h, http.MethodPost, "/api/auth/refresh", "192.0.2.9:2", `{"refresh_token":"`+login.RefreshToken+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var next struct {
		Token        string `json:"token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if !store.ValidToken(next.Token) || !store.ValidToken(next.RefreshToken) || next.RefreshToken == login.RefreshToken || next.Token == "" {
		t.Fatal(rec.Body.String())
	}
	stale := do(h, http.MethodPost, "/api/auth/refresh", "192.0.2.9:3", `{"refresh_token":"`+login.RefreshToken+`"}`)
	if stale.Code != http.StatusUnauthorized || !strings.Contains(stale.Body.String(), `"error":"unauthorized"`) {
		t.Fatal(stale.Code, stale.Body.String())
	}
	bad := do(h, http.MethodPost, "/api/auth/refresh", "192.0.2.9:4", `{"refresh_token":"nope"}`)
	if bad.Code != http.StatusUnauthorized {
		t.Fatal(bad.Code, bad.Body.String())
	}
	me := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+next.Token)
	h.ServeHTTP(me, req)
	if me.Code != http.StatusOK {
		t.Fatal(me.Code, me.Body.String())
	}
	expired := strings.Repeat("ab", 32)
	if err := db.InsertRefresh(expired, login.Admin.ID, "2000-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	expRec := do(h, http.MethodPost, "/api/auth/refresh", "192.0.2.9:5", `{"refresh_token":"`+expired+`"}`)
	if expRec.Code != http.StatusUnauthorized {
		t.Fatal(expRec.Code, expRec.Body.String())
	}
	expRec = do(h, http.MethodPost, "/api/auth/refresh", "192.0.2.9:6", `{"refresh_token":"`+expired+`"}`)
	if expRec.Code != http.StatusUnauthorized {
		t.Fatal(expRec.Code, expRec.Body.String())
	}
}
