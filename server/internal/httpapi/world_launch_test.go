package httpapi

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"modupdater/internal/chunkbackup"
	"modupdater/internal/store"
)

func TestUploadWorldAndLaunch(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	e.serverLayout(t)
	root := filepath.Dir(e.modsDir)
	writeText(t, filepath.Join(e.modsDir, "a.txt"), "mod")
	writeText(t, filepath.Join(root, "world", "level.dat"), "old")
	writeText(t, filepath.Join(root, "config", "c.txt"), "cfg")

	empty := zipBytes(t, map[string]string{"readme.txt": "no"})
	rec := e.do(http.MethodPost, "/api/admin/backups/world", tok, "application/zip", bytes.NewReader(empty))
	if !strings.Contains(rec.Body.String(), "В архиве нет level.dat.") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	slip := zipBytes(t, map[string]string{"../evil.txt": "x", "level.dat": "nope"})
	rec = e.do(http.MethodPost, "/api/admin/backups/world", tok, "application/zip", bytes.NewReader(slip))
	if !strings.Contains(rec.Body.String(), "Архив мира повреждён.") {
		t.Fatal(rec.Code, rec.Body.String())
	}

	body := zipBytes(t, map[string]string{"level.dat": "new", "region/r.0.0.mca": "chunk"})
	rec = e.do(http.MethodPost, "/api/admin/backups/world", tok, "application/zip", bytes.NewReader(body))
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var uploaded struct {
		Backup store.Backup `json:"backup"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	if uploaded.Backup.Label != "загруженный" || uploaded.Backup.Current {
		t.Fatalf("%+v", uploaded.Backup)
	}
	worldFile := filepath.Join(e.data, "backups", uploaded.Backup.ID, "world", "level.dat")
	if readText(t, worldFile) != "new" {
		t.Fatal(readText(t, worldFile))
	}
	if _, err := os.Stat(filepath.Join(e.data, "backups", uploaded.Backup.ID, "mods")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.data, "backups", uploaded.Backup.ID, "config")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	listed := e.do(http.MethodGet, "/api/admin/backups", tok, "", nil)
	if !strings.Contains(listed.Body.String(), uploaded.Backup.ID) || !strings.Contains(listed.Body.String(), "загруженный") {
		t.Fatal(listed.Body.String())
	}

	writeText(t, filepath.Join(root, "world", "level.dat"), "changed")
	if rec := e.do(http.MethodPost, "/api/admin/backups/milestone", tok, "", nil); rec.Code != http.StatusCreated {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var said []string
	var cmds []string
	var restarted bool
	e.mc.Say = func(command string) error {
		said = append(said, command)
		return nil
	}
	e.mc.Exec = func(command string) (string, error) {
		cmds = append(cmds, command)
		return "", nil
	}
	e.mc.Run = func(name string, args []string) error {
		restarted = name == "systemctl" && len(args) == 2 && args[0] == "restart" && args[1] == "minecraft.service"
		return nil
	}
	rec = e.do(http.MethodPost, "/api/admin/backups/"+uploaded.Backup.ID+"/launch", tok, "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"restarted":true`) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if !restarted || strings.Join(cmds, ",") != "save-off,save-all,save-on" {
		t.Fatal(restarted, cmds)
	}
	if len(said) < 3 || said[0] != "say Перезапуск сервера через 15 секунд: обновление модов" || said[1] != "say Началось сохранение мира" || said[len(said)-1] != "say Сохранение мира закончилось" {
		t.Fatal(said)
	}
	if readText(t, filepath.Join(root, "world", "level.dat")) != "new" {
		t.Fatal(readText(t, filepath.Join(root, "world", "level.dat")))
	}
	if readText(t, filepath.Join(e.modsDir, "a.txt")) != "mod" || readText(t, filepath.Join(root, "config", "c.txt")) != "cfg" {
		t.Fatal("live mods or config changed")
	}
	var launched struct {
		Backups []store.Backup `json:"backups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &launched); err != nil {
		t.Fatal(err)
	}
	sawSafety := false
	sawUpload := false
	for _, item := range launched.Backups {
		if item.Label == "перед запуском" {
			sawSafety = true
			saved := filepath.Join(restoreChunk(t, e.data, item.ID), "world", "level.dat")
			if readText(t, saved) != "changed" {
				t.Fatal(readText(t, saved))
			}
		}
		if item.ID == uploaded.Backup.ID && item.Current {
			sawUpload = true
		}
	}
	if !sawSafety || !sawUpload {
		t.Fatalf("%+v", launched.Backups)
	}
}

func TestDownloadBackupZip(t *testing.T) {
	e := newEnv(t)
	missing := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if rec := e.do(http.MethodGet, "/api/admin/backups/"+missing+"/file", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatal(rec.Code, rec.Body.String())
	}
	tok := e.token(t)
	rec := e.do(http.MethodGet, "/api/admin/backups/nope/file", tok, "", nil)
	if !strings.Contains(rec.Body.String(), "Бекап не найден.") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodGet, "/api/admin/backups/"+missing+"/file", tok, "", nil)
	if !strings.Contains(rec.Body.String(), "Бекап не найден.") {
		t.Fatal(rec.Code, rec.Body.String())
	}

	e.setMods(t, tok)
	e.serverLayout(t)
	root := filepath.Dir(e.modsDir)
	writeText(t, filepath.Join(e.modsDir, "a.txt"), "mod")
	writeText(t, filepath.Join(root, "config", "c.txt"), "cfg")
	writeText(t, filepath.Join(root, "world", "level.dat"), "old")
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
	rec = e.do(http.MethodGet, "/api/admin/backups/"+created.Backup.ID+"/file", tok, "", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zstd" {
		t.Fatal(rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	wantName := "backup-" + digitsOnly(created.Backup.CreatedAt) + "-" + created.Backup.ID + ".tar.zst"
	if rec.Header().Get("Content-Disposition") != `attachment; filename="`+wantName+`"` {
		t.Fatal(rec.Header().Get("Content-Disposition"))
	}
	if rec.Header().Get("Content-Length") != "" {
		t.Fatal(rec.Header().Get("Content-Length"))
	}
	files := mergedFiles(t, rec.Body.Bytes())
	if files["mods/a.txt"] != "mod" || files["config/c.txt"] != "cfg" || files["world/level.dat"] != "old" {
		t.Fatalf("%v", files)
	}

	body := zipBytes(t, map[string]string{"level.dat": "only"})
	rec = e.do(http.MethodPost, "/api/admin/backups/world", tok, "application/zip", bytes.NewReader(body))
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var uploaded struct {
		Backup store.Backup `json:"backup"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	rec = e.do(http.MethodGet, "/api/admin/backups/"+uploaded.Backup.ID+"/file", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Length") != "" {
		t.Fatal(rec.Header().Get("Content-Length"))
	}
	only := tarZstNames(t, rec.Body.Bytes())
	if only["world/level.dat"] != "only" {
		t.Fatalf("%v", only)
	}
	for name := range only {
		if strings.HasPrefix(name, "mods/") || strings.HasPrefix(name, "config/") {
			t.Fatal(name)
		}
	}
}

func mergedFiles(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "m.tar.zst")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := chunkbackup.Merge(path, "", out); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	err := filepath.WalkDir(out, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(out, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func tarZstNames(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	zr, err := zstd.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[hdr.Name] = string(body)
	}
	return out
}

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
