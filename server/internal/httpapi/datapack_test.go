package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modupdater/internal/mods"
)

func TestAdminDatapackUpload(t *testing.T) {
	e := newEnv(t)
	root := filepath.Dir(e.modsDir)
	if err := os.WriteFile(filepath.Join(root, "server.properties"), []byte("level-name=NewWorld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "NewWorld"), 0o755); err != nil {
		t.Fatal(err)
	}
	decoy := filepath.Join(root, "world", "datapacks", "decoy")
	if err := os.MkdirAll(decoy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(decoy, "pack.mcmeta"), []byte("decoy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(http.MethodPost, "/api/admin/datapacks", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatal(rec.Code, rec.Body.String())
	}
	tok := e.token(t)
	e.setMods(t, tok)

	packs := filepath.Join(root, "NewWorld", "datapacks")
	bad, ctype := multipartBody(t, "nope.zip", datapackArchive(t, map[string]string{"readme.txt": "no"}), nil)
	rec := e.do(http.MethodPost, "/api/admin/datapacks", tok, ctype, bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var errBody struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Error != mods.ErrNotDatapack.Error() {
		t.Fatal(errBody.Error)
	}
	if _, err := os.Stat(packs); !os.IsNotExist(err) {
		t.Fatal(err)
	}

	good, ctype := multipartBody(t, "quest.zip", datapackArchive(t, map[string]string{
		"Wrapped/pack.mcmeta": `{"pack":{"pack_format":48,"description":"Тестовый пак"}}`,
		"Wrapped/data/a.txt":  "yes",
	}), nil)
	rec = e.do(http.MethodPost, "/api/admin/datapacks", tok, ctype, good)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var created struct {
		Datapack mods.Datapack `json:"datapack"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Datapack.Name != "quest" || created.Datapack.Description != "Тестовый пак" {
		t.Fatalf("%+v", created.Datapack)
	}
	quest := filepath.Join(packs, "quest")
	body, err := os.ReadFile(filepath.Join(quest, "data", "a.txt"))
	if err != nil || string(body) != "yes" {
		t.Fatal(err, string(body))
	}
	if _, err := os.Stat(filepath.Join(quest, "Wrapped")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	decoyBody, err := os.ReadFile(filepath.Join(decoy, "pack.mcmeta"))
	if err != nil || string(decoyBody) != "decoy" {
		t.Fatal(err, string(decoyBody))
	}
	rec = e.do(http.MethodGet, "/api/admin/datapacks", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var list struct {
		Datapacks []mods.Datapack `json:"datapacks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Datapacks) != 1 || list.Datapacks[0].Name != "quest" || list.Datapacks[0].Description != "Тестовый пак" {
		t.Fatalf("%+v", list.Datapacks)
	}
}

func datapackArchive(t *testing.T, files map[string]string) []byte {
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

func TestAdminDatapackFilePath(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	root := filepath.Dir(e.modsDir)
	if err := os.WriteFile(filepath.Join(root, "server.properties"), []byte("level-name=world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	quest := filepath.Join(root, "world", "datapacks", "quest", "data")
	if err := os.MkdirAll(quest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(quest, "foo.json"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "world", "datapacks", "other.txt")
	if err := os.WriteFile(outside, []byte("stay"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := e.do(http.MethodPut, "/api/admin/datapacks/quest/file", tok, "application/json", strings.NewReader(`{"path":"data/foo.json","text":"new"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code, rec.Body.String())
	}
	body, err := os.ReadFile(filepath.Join(quest, "foo.json"))
	if err != nil || string(body) != "new" {
		t.Fatal(err, string(body))
	}
	rec = e.do(http.MethodPut, "/api/admin/datapacks/quest/file", tok, "application/json", strings.NewReader(`{"path":"../other.txt","text":"hack"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var errBody struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatal(err)
	}
	if errBody.Error != mods.ErrDatapackPath.Error() {
		t.Fatal(errBody.Error)
	}
	outsideBody, err := os.ReadFile(outside)
	if err != nil || string(outsideBody) != "stay" {
		t.Fatal(err, string(outsideBody))
	}
}

func TestAdminDatapackAccessSentence(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root")
	}
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	root := filepath.Dir(e.modsDir)
	if err := os.WriteFile(filepath.Join(root, "server.properties"), []byte("level-name=world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	packs := filepath.Join(root, "world", "datapacks")
	if err := os.MkdirAll(packs, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(packs, 0o755) })
	if err := os.Chmod(packs, 0); err != nil {
		t.Fatal(err)
	}
	rec := e.do(http.MethodGet, "/api/admin/datapacks", tok, "", nil)
	if rec.Code != http.StatusBadGateway {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error == "internal" || body.Error == "Нет доступа к каталогу сервера." || !strings.Contains(body.Error, packs) {
		t.Fatal(body.Error)
	}
}
