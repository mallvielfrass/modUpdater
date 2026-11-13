package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestUpdateModMetaKeepsJar(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	content := jarBytes(t, "srv", "1", "Srv", "d")
	created := addAdminJar(t, e, tok, "Srv.jar", content, nil)
	path := filepath.Join(e.modsDir, "Srv.jar")
	body, ctype := multipartBody(t, "", nil, map[string]string{"name": "New", "client_only": "true"})
	rec := e.do(http.MethodPut, "/api/admin/mods/"+created.ID, tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var updated struct {
		Mod modJSON `json:"mod"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Mod.Name != "New" || updated.Mod.ClientOnly || updated.Mod.Filename != "Srv.jar" {
		t.Fatalf("%+v", updated.Mod)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.data, "client_mods", "Srv.jar")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestUpdateModJarMode(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	created := addAdminJar(t, e, tok, "Srv.jar", jarBytes(t, "srv", "1", "Srv", "d"), nil)
	path := filepath.Join(e.modsDir, "Srv.jar")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	next := jarBytes(t, "srv", "2", "Srv", "d2")
	body, ctype := multipartBody(t, "Srv.jar", next, nil)
	rec := e.do(http.MethodPut, "/api/admin/mods/"+created.ID, tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, next) {
		t.Fatal(err)
	}
	if mode := filePerm(t, path); mode != 0o644 {
		t.Fatalf("mode %o", mode)
	}
}

func TestUpdateUnreadableJarReplaces(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root")
	}
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	created := addAdminJar(t, e, tok, "Srv.jar", jarBytes(t, "srv", "1", "Srv", "d"), nil)
	path := filepath.Join(e.modsDir, "Srv.jar")
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	next := jarBytes(t, "srv", "2", "Srv", "d2")
	body, ctype := multipartBody(t, "Srv.jar", next, nil)
	rec := e.do(http.MethodPut, "/api/admin/mods/"+created.ID, tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, next) {
		t.Fatal(err)
	}
	if mode := filePerm(t, path); mode != 0o644 {
		t.Fatalf("mode %o", mode)
	}
}

func TestUpdateClientOnlySkipsModsDir(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	created := addAdminJar(t, e, tok, "Only.jar", jarBytes(t, "only", "1", "Only", "d"), map[string]string{"client_only": "true"})
	missing := filepath.Join(t.TempDir(), "missing-mods")
	if err := e.db.SetModsDir(missing); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(e.modsDir, "canary.txt")
	if err := os.WriteFile(canary, []byte("stay"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, ctype := multipartBody(t, "", nil, map[string]string{"name": "Renamed"})
	rec := e.do(http.MethodPut, "/api/admin/mods/"+created.ID, tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	next := jarBytes(t, "only", "2", "Only", "d2")
	body, ctype = multipartBody(t, "Only.jar", next, nil)
	rec = e.do(http.MethodPut, "/api/admin/mods/"+created.ID, tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	live := filepath.Join(e.data, "client_mods", "Only.jar")
	got, err := os.ReadFile(live)
	if err != nil || !bytes.Equal(got, next) {
		t.Fatal(err)
	}
	if mode := filePerm(t, live); mode != 0o644 {
		t.Fatalf("mode %o", mode)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.modsDir, "Only.jar")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(canary)
	if err != nil || string(raw) != "stay" {
		t.Fatal(err, string(raw))
	}
	rec = e.do(http.MethodDelete, "/api/admin/mods/"+created.ID, tok, "", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestUpdateClientRequiredDoesNotMoveFile(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	content := jarBytes(t, "only", "1", "Only", "d")
	created := addAdminJar(t, e, tok, "Only.jar", content, map[string]string{"client_only": "true", "client_required": "false"})
	missing := filepath.Join(t.TempDir(), "missing-mods")
	if err := e.db.SetModsDir(missing); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.data, "client_mods", "Only.jar")
	ino := fileInode(t, path)
	body, ctype := multipartBody(t, "", nil, map[string]string{"client_required": "true", "client_only": "false"})
	rec := e.do(http.MethodPut, "/api/admin/mods/"+created.ID, tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var updated struct {
		Mod modJSON `json:"mod"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if !updated.Mod.ClientOnly || !updated.Mod.ClientRequired || updated.Mod.Filename != "Only.jar" {
		t.Fatalf("%+v", updated.Mod)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal(err)
	}
	if fileInode(t, path) != ino {
		t.Fatal("file moved")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.modsDir, "Only.jar")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestUpdateModDirDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root")
	}
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	content := jarBytes(t, "srv", "1", "Srv", "d")
	created := addAdminJar(t, e, tok, "Srv.jar", content, nil)
	path := filepath.Join(e.modsDir, "Srv.jar")
	t.Cleanup(func() { _ = os.Chmod(e.modsDir, 0o755) })
	if err := os.Chmod(e.modsDir, 0o555); err != nil {
		t.Fatal(err)
	}
	body, ctype := multipartBody(t, "", nil, map[string]string{"name": "Renamed"})
	rec := e.do(http.MethodPut, "/api/admin/mods/"+created.ID, tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	body, ctype = multipartBody(t, "Srv.jar", jarBytes(t, "srv", "2", "Srv", "d2"), nil)
	rec = e.do(http.MethodPut, "/api/admin/mods/"+created.ID, tok, ctype, body)
	if rec.Code != http.StatusBadGateway || errorCode(t, rec) == "internal" || !strings.Contains(errorCode(t, rec), e.modsDir) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(errorCode(t, rec), "Нет доступа к каталогу ") {
		t.Fatal(rec.Body.String())
	}
	_ = os.Chmod(e.modsDir, 0o755)
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal(err)
	}
}

func TestUpdateClientModsDirDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root")
	}
	e := newEnv(t)
	tok := e.token(t)
	content := jarBytes(t, "only", "1", "Only", "d")
	created := addAdminJar(t, e, tok, "Only.jar", content, map[string]string{"client_only": "true"})
	dir := filepath.Join(e.data, "client_mods")
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	body, ctype := multipartBody(t, "Only.jar", jarBytes(t, "only", "2", "Only", "d2"), nil)
	rec := e.do(http.MethodPut, "/api/admin/mods/"+created.ID, tok, ctype, body)
	if rec.Code != http.StatusBadGateway || errorCode(t, rec) == "internal" || !strings.Contains(errorCode(t, rec), dir) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	_ = os.Chmod(dir, 0o755)
	got, err := os.ReadFile(filepath.Join(dir, "Only.jar"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal(err)
	}
}

func TestUpdateModTargetNotFile(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	created := addAdminJar(t, e, tok, "Srv.jar", jarBytes(t, "srv", "1", "Srv", "d"), nil)
	path := filepath.Join(e.modsDir, "Srv.jar")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	body, ctype := multipartBody(t, "Srv.jar", jarBytes(t, "srv", "2", "Srv", "d2"), nil)
	rec := e.do(http.MethodPut, "/api/admin/mods/"+created.ID, tok, ctype, body)
	code := errorCode(t, rec)
	if rec.Code != http.StatusBadGateway || code == "internal" || !strings.Contains(code, path) || !strings.HasPrefix(code, "Не удалось записать файл ") {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestUpdateBadJarKeepsOld(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	content := jarBytes(t, "srv", "1", "Srv", "d")
	created := addAdminJar(t, e, tok, "Srv.jar", content, nil)
	body, ctype := multipartBody(t, "Srv.jar", badMethodJar(t), nil)
	rec := e.do(http.MethodPut, "/api/admin/mods/"+created.ID, tok, ctype, body)
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "Не удалось прочитать файл мода." {
		t.Fatal(rec.Code, rec.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(e.modsDir, "Srv.jar"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal(err)
	}
}

func TestUpdateClientModsSymlink(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	created := addAdminJar(t, e, tok, "Only.jar", jarBytes(t, "only", "1", "Only", "d"), map[string]string{"client_only": "true"})
	live := filepath.Join(e.data, "client_mods")
	held := filepath.Join(t.TempDir(), "held")
	if err := os.Rename(live, held); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, live); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(live) })
	body, ctype := multipartBody(t, "", nil, map[string]string{"name": "X"})
	rec := e.do(http.MethodPut, "/api/admin/mods/"+created.ID, tok, ctype, body)
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "Некорректный путь файла." {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func TestUpdateUnknownMod(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	body, ctype := multipartBody(t, "", nil, map[string]string{"name": "X"})
	rec := e.do(http.MethodPut, "/api/admin/mods/0123456789abcdef0123456789abcdef", tok, ctype, body)
	if rec.Code != http.StatusNotFound || errorCode(t, rec) != "not_found" {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func addAdminJar(t *testing.T, e *env, tok, filename string, content []byte, fields map[string]string) modJSON {
	t.Helper()
	body, ctype := multipartBody(t, filename, content, fields)
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
	return created.Mod
}

func filePerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func fileInode(t *testing.T, path string) uint64 {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatal(err)
	}
	return st.Ino
}

func badMethodJar(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("fabric.mod.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`{"id":"broken","version":"9","name":"Broken"}`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	patchZipMethod(t, b, []byte{0x50, 0x4b, 0x03, 0x04}, 8)
	patchZipMethod(t, b, []byte{0x50, 0x4b, 0x01, 0x02}, 10)
	return b
}

func patchZipMethod(t *testing.T, b, sig []byte, off int) {
	t.Helper()
	i := bytes.Index(b, sig)
	if i < 0 || i+off+1 >= len(b) {
		t.Fatal("zip header")
	}
	b[i+off] = 99
	b[i+off+1] = 0
}
