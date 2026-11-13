package minecraft_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"modupdater/internal/auth"
	"modupdater/internal/config"
	"modupdater/internal/httpapi"
	"modupdater/internal/minecraft"
	"modupdater/internal/mods"
	"modupdater/internal/store"
)

func TestSayCommand(t *testing.T) {
	got := minecraft.SayCommand("hello\nworld\r!\x00", "warn")
	if got != "say helloworld!" || strings.ContainsAny(got, "\n\r\x00") {
		t.Fatalf("%q", got)
	}
	if strings.Count(got, "say ") != 1 {
		t.Fatal(got)
	}
	got = minecraft.SayCommand(strings.Repeat("я", 250), "w")
	body := strings.TrimPrefix(got, "say ")
	if !strings.HasPrefix(got, "say ") || len([]rune(body)) != 200 {
		t.Fatalf("%d %q", len([]rune(body)), got)
	}
	if minecraft.SayCommand("", "Перезапуск") != "say Перезапуск" {
		t.Fatal(got)
	}
}

func TestEmptyRestartUsesWarningAndArgs(t *testing.T) {
	warning := "Перезапуск сервера через 15 секунд: обновление модов"
	s := minecraft.New("minecraft.service", warning, 0, "127.0.0.1", 25575, "secret")
	var said string
	s.Say = func(command string) error {
		said = command
		return nil
	}
	s.Run = func(name string, args []string) error {
		if name != "systemctl" || len(args) != 2 || args[0] != "restart" || args[1] != "minecraft.service" {
			t.Fatalf("%s %#v", name, args)
		}
		if name == "sh" || (len(args) > 0 && args[0] == "-c") {
			t.Fatal("shell")
		}
		return nil
	}
	if err := s.Restart(""); err != nil {
		t.Fatal(err)
	}
	if said != "say "+warning {
		t.Fatalf("%q", said)
	}
}

func TestJournalArgs(t *testing.T) {
	s := minecraft.New("minecraft.service", "w", 0, "127.0.0.1", 25575, "secret")
	s.Output = func(name string, args []string) (string, error) {
		if name != "journalctl" {
			t.Fatal(name)
		}
		want := []string{"-u", "minecraft.service", "-n", "200", "-o", "short-iso", "--no-pager"}
		if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
			t.Fatalf("%#v", args)
		}
		return "one\ntwo\n", nil
	}
	unit, lines, err := s.Logs(200, "")
	if err != nil || unit != "minecraft.service" || len(lines) != 2 || lines[0] != "one" {
		t.Fatalf("%s %v %v", unit, lines, err)
	}
}

func TestBadUnitRejectedAtConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{
  "listen": ":8080",
  "data_dir": "./data",
  "public_url": "http://127.0.0.1:8080",
  "root_admin": { "username": "admin", "password": "" },
  "minecraft": {
    "systemd_unit": "minecraft.service;touch",
    "restart_warning": "w",
    "restart_delay_sec": 15,
    "rcon": { "host": "127.0.0.1", "port": 25575, "password": "" }
  }
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err == nil {
		t.Fatal("accepted bad unit")
	}
}

func TestFailedSayDoesNotRestart(t *testing.T) {
	s := minecraft.New("minecraft.service", "w", 0, "127.0.0.1", 25575, "secret")
	s.Say = func(string) error { return errors.New("dial") }
	s.Run = func(string, []string) error {
		t.Fatal("restart runner called")
		return nil
	}
	if err := s.Restart("hi"); !errors.Is(err, minecraft.ErrRCON) {
		t.Fatal(err)
	}
}

func TestOverlappingRestart(t *testing.T) {
	s := minecraft.New("minecraft.service", "w", 0, "127.0.0.1", 25575, "secret")
	started := make(chan struct{})
	release := make(chan struct{})
	s.Say = func(string) error {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return nil
	}
	s.Run = func(string, []string) error { return nil }
	errCh := make(chan error, 1)
	go func() { errCh <- s.Restart("a") }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
	if err := s.Restart("b"); !errors.Is(err, minecraft.ErrBusy) {
		t.Fatal(err)
	}
	close(release)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestPrepareWorldCopyOrder(t *testing.T) {
	s := minecraft.New("minecraft.service", "w", 0, "", 0, "")
	if err := s.PrepareWorldCopy(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	s = minecraft.New("minecraft.service", "w", 0, "127.0.0.1", 25575, "secret")
	s.Say = func(string) error { return errors.New("down") }
	s.Exec = func(string) (string, error) {
		t.Fatal("save")
		return "", nil
	}
	copied := false
	if err := s.PrepareWorldCopy(func() error { copied = true; return nil }); err != nil || !copied {
		t.Fatal(err, copied)
	}
	var steps []string
	s.Say = func(command string) error {
		steps = append(steps, command)
		return nil
	}
	s.Exec = func(command string) (string, error) {
		steps = append(steps, command)
		if command == "save-all" {
			return "", errors.New("slow")
		}
		return "", nil
	}
	err := s.PrepareWorldCopy(func() error {
		steps = append(steps, "copy")
		return errors.New("Не удалось скопировать файлы сервера.")
	})
	if err == nil || err.Error() != "Не удалось скопировать файлы сервера." {
		t.Fatal(err)
	}
	want := "say Началось сохранение мира,save-off,save-all,copy,save-on,say Сохранение мира закончилось"
	if strings.Join(steps, ",") != want {
		t.Fatal(steps)
	}
}

func TestRestartPreparedStopsBeforeSystemctl(t *testing.T) {
	s := minecraft.New("minecraft.service", "Предупреждение", 0, "127.0.0.1", 25575, "secret")
	s.Say = func(string) error { return nil }
	called := false
	s.Run = func(string, []string) error {
		called = true
		return nil
	}
	err := s.RestartPrepared("", func() error { return errors.New("Нет доступа к каталогу сервера.") })
	if called || err == nil || err.Error() != "Нет доступа к каталогу сервера." {
		t.Fatal(called, err)
	}
}

func TestSnapshotErrorStillRestarts(t *testing.T) {
	s := minecraft.New("minecraft.service", "w", 0, "127.0.0.1", 25575, "secret")
	s.Say = func(string) error { return nil }
	called := false
	s.Run = func(name string, args []string) error {
		called = name == "systemctl" && len(args) == 2 && args[0] == "restart" && args[1] == "minecraft.service"
		return nil
	}
	err := s.RestartBefore("", func() error { return errors.New("backup_failed") })
	if !called || err == nil || err.Error() != "backup_failed" {
		t.Fatal(called, err)
	}
}

func TestActiveEnterUsesRestartRunner(t *testing.T) {
	s := minecraft.New("minecraft.service", "w", 0, "127.0.0.1", 25575, "secret")
	var calls []string
	s.Output = func(name string, args []string) (string, error) {
		if name != "systemctl" || len(args) == 0 || args[0] != "show" || args[1] != "minecraft.service" {
			t.Fatalf("%s %#v", name, args)
		}
		if name == "sh" || (len(args) > 0 && args[0] == "-c") {
			t.Fatal("shell")
		}
		calls = append(calls, strings.Join(append([]string{name}, args...), " "))
		if strings.Contains(strings.Join(args, " "), "--timestamp=unix") {
			return "@1793831934\n", nil
		}
		t.Fatal("fallback")
		return "", nil
	}
	at, ok := s.ActiveEnter()
	if !ok || at.Unix() != 1793831934 || len(calls) != 1 {
		t.Fatal(ok, at, calls)
	}
	s.Output = func(name string, args []string) (string, error) {
		if name != "systemctl" {
			t.Fatal(name)
		}
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "--timestamp=unix") {
			return "", errors.New("unavailable")
		}
		return "Thu 2026-11-05 04:17:00 MSK\n", nil
	}
	at, ok = s.ActiveEnter()
	want, err := time.ParseInLocation("Mon 2006-01-02 15:04:05", "Thu 2026-11-05 04:17:00", time.Local)
	if err != nil || !ok || !at.Equal(want.UTC()) {
		t.Fatal(ok, at, want, err)
	}
	s.Output = func(string, []string) (string, error) { return "", errors.New("unavailable") }
	if _, ok = s.ActiveEnter(); ok {
		t.Fatal("available")
	}
	s.Output = func(name string, args []string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "--timestamp=unix") {
			return "0\n", nil
		}
		return "n/a\n", nil
	}
	if _, ok = s.ActiveEnter(); ok {
		t.Fatal("zero")
	}
}

func TestRestartAndLogsHandlers(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	mcDir := filepath.Join(root, "mc")
	if err := os.MkdirAll(filepath.Join(data, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mcDir, "mods"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mcDir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mcDir, "world"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mcDir, "server.properties"), []byte("level-name=world\n"), 0o644); err != nil {
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
	if err := db.SetModsDir(filepath.Join(mcDir, "mods")); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		DataDir:   data,
		PublicURL: "http://127.0.0.1:8080",
		Minecraft: config.Minecraft{SystemdUnit: "minecraft.service", RestartWarning: "Перезапуск сервера через 15 секунд: обновление модов"},
	}
	mc := minecraft.New("minecraft.service", cfg.Minecraft.RestartWarning, 0, "127.0.0.1", 25575, "secret")
	var mu sync.Mutex
	var ran []string
	mc.Run = func(name string, args []string) error {
		mu.Lock()
		ran = append([]string{name}, args...)
		mu.Unlock()
		return nil
	}
	var said []string
	mc.Say = func(command string) error {
		said = append(said, command)
		return nil
	}
	var rconCmds []string
	mc.Exec = func(command string) (string, error) {
		rconCmds = append(rconCmds, command)
		return "", nil
	}
	var got []string
	mc.Output = func(name string, args []string) (string, error) {
		if name != "journalctl" {
			t.Fatalf("%s %#v", name, args)
		}
		got = append([]string(nil), args...)
		return "line\n", nil
	}
	api := httpapi.New(cfg, au, mods.New(db, data), mc)
	tok := handlerLogin(t, api)

	rec := handlerDo(api, http.MethodPost, "/api/admin/server/restart", tok, `{"message":"","systemd_unit":"evil.service","rcon":{"password":"nope"}}`)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if len(said) == 0 || said[0] != "say "+cfg.Minecraft.RestartWarning || len(rconCmds) != 0 {
		t.Fatalf("%v %v", said, rconCmds)
	}
	if !strings.Contains(rec.Body.String(), "Сначала создайте веху.") {
		t.Fatal(rec.Body.String())
	}
	if strings.Join(ran, " ") != "systemctl restart minecraft.service" {
		t.Fatalf("%v", ran)
	}

	mc.Say = func(string) error { return errors.New("auth") }
	called := false
	mc.Run = func(string, []string) error { called = true; return nil }
	rec = handlerDo(api, http.MethodPost, "/api/admin/server/restart", tok, `{"message":"hi"}`)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), `"error":"rcon_failed"`) || called {
		t.Fatal(rec.Code, rec.Body.String(), called)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	mc.Say = func(string) error {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return nil
	}
	mc.Run = func(string, []string) error { return nil }
	done := make(chan int, 1)
	go func() {
		done <- handlerDo(api, http.MethodPost, "/api/admin/server/restart", tok, `{"message":"hold"}`).Code
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
	rec = handlerDo(api, http.MethodPost, "/api/admin/server/restart", tok, `{"message":"other"}`)
	if !strings.Contains(rec.Body.String(), `"error":"restart_in_progress"`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	close(release)
	if code := <-done; code != http.StatusOK {
		t.Fatal(code)
	}

	rec = handlerDo(api, http.MethodGet, "/api/admin/server/logs", tok, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"unit":"minecraft.service"`) {
		t.Fatal(rec.Body.String())
	}
	if strings.Join(got, " ") != "-u minecraft.service -n 200 -o short-iso --no-pager" {
		t.Fatalf("%v", got)
	}
	rec = handlerDo(api, http.MethodGet, "/api/admin/server/logs?since=hour", tok, "")
	if rec.Code != http.StatusOK || strings.Join(got, " ") != "-u minecraft.service -n 200 -o short-iso --no-pager --since 1 hour ago" {
		t.Fatal(rec.Code, rec.Body.String(), got)
	}
	rec = handlerDo(api, http.MethodGet, "/api/admin/server/logs?since=day", tok, "")
	if rec.Code != http.StatusOK || strings.Join(got, " ") != "-u minecraft.service -n 200 -o short-iso --no-pager --since 1 day ago" {
		t.Fatal(rec.Code, rec.Body.String(), got)
	}
	rec = handlerDo(api, http.MethodGet, "/api/admin/server/logs?since=nope", tok, "")
	if !strings.Contains(rec.Body.String(), `"error":"invalid_limit"`) {
		t.Fatal(rec.Body.String())
	}
	rec = handlerDo(api, http.MethodGet, "/api/admin/server/logs?lines=0", tok, "")
	if !strings.Contains(rec.Body.String(), `"error":"invalid_limit"`) {
		t.Fatal(rec.Body.String())
	}
	rec = handlerDo(api, http.MethodGet, "/api/admin/server/logs?lines=5001", tok, "")
	if !strings.Contains(rec.Body.String(), `"error":"invalid_limit"`) {
		t.Fatal(rec.Body.String())
	}

	mc.Exec = func(command string) (string, error) {
		switch command {
		case "list":
			return "There are 1 of a max of 20 players online: Ann", nil
		case "forge mods":
			return "Mod List: \n• JAVA_FML forge.jar : forge (47.4.18) - 1", nil
		default:
			t.Fatalf("%s", command)
			return "", errors.New("cmd")
		}
	}
	rec = handlerDo(api, http.MethodGet, "/api/admin/server/rcon", tok, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"up":true`) || !strings.Contains(rec.Body.String(), `"Ann"`) || !strings.Contains(rec.Body.String(), `"id":"forge"`) {
		t.Fatal(rec.Body.String())
	}
	mc.Exec = func(string) (string, error) { return "", minecraft.ErrRCON }
	rec = handlerDo(api, http.MethodGet, "/api/admin/server/rcon", tok, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"up":false`) {
		t.Fatal(rec.Body.String())
	}

	if err := os.RemoveAll(filepath.Join(data, "backups")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "backups"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mc.Say = func(string) error { return nil }
	restarted := false
	mc.Run = func(name string, args []string) error {
		restarted = name == "systemctl" && len(args) == 2 && args[0] == "restart" && args[1] == "minecraft.service"
		return nil
	}
	rec = handlerDo(api, http.MethodPost, "/api/admin/server/restart", tok, `{"message":""}`)
	if rec.Code != http.StatusOK || !restarted || !strings.Contains(rec.Body.String(), `"restarted":true`) || !strings.Contains(rec.Body.String(), "Сначала создайте веху.") {
		t.Fatal(rec.Code, restarted, rec.Body.String())
	}
}

func handlerLogin(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"adminpass1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.70:1"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Token
}

func handlerDo(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	req.RemoteAddr = "192.0.2.71:1"
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	h.ServeHTTP(rec, req)
	return rec
}
