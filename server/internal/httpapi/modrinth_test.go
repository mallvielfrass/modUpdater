package httpapi

import (
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRevokeRemovesFromClientList(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	body, ctype := multipartBody(t, "Only.jar", jarBytes(t, "only", "1", "Only", "d"), map[string]string{"client_only": "true"})
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
	rec = e.do(http.MethodPost, "/api/admin/mods/"+created.Mod.ID+"/revoke", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	pub := e.do(http.MethodGet, "/api/mods", "", "", nil)
	var list struct {
		Mods    []modJSON `json:"mods"`
		Revoked []struct {
			Filename string `json:"filename"`
			SHA256   string `json:"sha256"`
		} `json:"revoked"`
	}
	if err := json.Unmarshal(pub.Body.Bytes(), &list); err != nil {
		t.Fatal(pub.Body.String())
	}
	if len(list.Mods) != 0 || len(list.Revoked) != 1 || list.Revoked[0].Filename != "Only.jar" || list.Revoked[0].SHA256 != created.Mod.SHA256 {
		t.Fatalf("%s", pub.Body.String())
	}
	file := e.do(http.MethodGet, "/api/mods/"+created.Mod.ID+"/file", "", "", nil)
	if file.Code != http.StatusNotFound {
		t.Fatal(file.Code)
	}
	dl := e.do(http.MethodGet, "/api/mods/"+created.Mod.ID+"/download", "", "", nil)
	if dl.Code != http.StatusNotFound {
		t.Fatal(dl.Code)
	}
	stored := filepath.Join(e.data, "revoked", created.Mod.ID+".jar")
	blob, err := os.ReadFile(stored)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(blob)
	if hex.EncodeToString(sum[:]) != created.Mod.SHA256 {
		t.Fatal("stored hash")
	}
	if _, err := os.Stat(filepath.Join(e.data, "client_mods", "Only.jar")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(e.adminMods(t, tok).PendingRestart) != 0 {
		t.Fatal("client_only pending")
	}
	rec = e.do(http.MethodPost, "/api/admin/mods/"+created.Mod.ID+"/restore", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	back, err := os.ReadFile(filepath.Join(e.data, "client_mods", "Only.jar"))
	if err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256(back)
	if hex.EncodeToString(sum[:]) != created.Mod.SHA256 {
		t.Fatal("restored hash")
	}
	if len(e.adminMods(t, tok).PendingRestart) != 0 {
		t.Fatal("client_only pending after restore")
	}
	rec = e.do(http.MethodPost, "/api/admin/mods/"+created.Mod.ID+"/revoke", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	rec = e.do(http.MethodDelete, "/api/admin/mods/"+created.Mod.ID, tok, "", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(e.adminMods(t, tok).Mods) != 0 {
		t.Fatal("mod row remains")
	}
}

func TestImportModrinthSides(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	clientJar := jarBytes(t, "clientmod", "1", "Client", "d")
	serverJar := jarBytes(t, "servermod", "1", "Server", "d")
	commonJar := jarBytes(t, "commonmod", "1", "Common", "d")
	badJar := jarBytes(t, "badmod", "1", "Bad", "d")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/client":
			_, _ = w.Write(clientJar)
		case "/server":
			_, _ = w.Write(serverJar)
		case "/common":
			_, _ = w.Write(commonJar)
		default:
			_, _ = w.Write(badJar)
		}
	}))
	defer srv.Close()
	index := map[string]any{
		"files": []any{
			packEntry("mods/Client.jar", clientJar, "required", "unsupported", srv.URL+"/client"),
			packEntry("mods/Server.jar", serverJar, "unsupported", "required", srv.URL+"/server"),
			packEntry("mods/Common.jar", commonJar, "required", "required", srv.URL+"/common"),
			packEntry("mods/Bad.jar", clientJar, "required", "unsupported", srv.URL+"/bad"),
			packEntry("resourcepacks/x.zip", clientJar, "required", "required", srv.URL+"/skip"),
		},
	}
	raw, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	body, ctype := multipartBody(t, "modrinth.index.json", raw, nil)
	rec := e.do(http.MethodPost, "/api/admin/mods/modrinth", tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var result struct {
		Added        int      `json:"added"`
		AddedFiles   []string `json:"added_files"`
		Failed       []string `json:"failed"`
		Skipped      int      `json:"skipped"`
		SkippedFiles []string `json:"skipped_files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(rec.Body.String())
	}
	if result.Added != 3 || result.Skipped != 0 || len(result.Failed) != 1 || len(result.SkippedFiles) != 0 {
		t.Fatalf("%s", rec.Body.String())
	}
	if strings.Join(result.AddedFiles, ",") != "Client.jar,Server.jar,Common.jar" {
		t.Fatalf("%s", rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(e.data, "client_mods", "Client.jar")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.modsDir, "Server.jar")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.modsDir, "Common.jar")); err != nil {
		t.Fatal(err)
	}
	pub := e.do(http.MethodGet, "/api/mods", "", "", nil)
	var list struct {
		Mods []modJSON `json:"mods"`
	}
	if err := json.Unmarshal(pub.Body.Bytes(), &list); err != nil {
		t.Fatal(pub.Body.String())
	}
	got := map[string]modJSON{}
	for _, m := range list.Mods {
		got[m.Slug] = m
	}
	if _, ok := got["servermod"]; ok {
		t.Fatal("server mod is public")
	}
	if !got["clientmod"].ClientOnly || !got["commonmod"].Client || got["commonmod"].ClientOnly {
		t.Fatalf("%+v", got)
	}
	body, ctype = multipartBody(t, "modrinth.index.json", raw, nil)
	rec = e.do(http.MethodPost, "/api/admin/mods/modrinth", tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(rec.Body.String())
	}
	if result.Skipped != 3 || result.Added != 0 || len(result.AddedFiles) != 0 {
		t.Fatalf("%s", rec.Body.String())
	}
	if strings.Join(result.SkippedFiles, ",") != "Client.jar,Server.jar,Common.jar" {
		t.Fatalf("%s", rec.Body.String())
	}
}

func packEntry(path string, content []byte, client, server, link string) map[string]any {
	s1 := sha1.Sum(content)
	s512 := sha512.Sum512(content)
	return map[string]any{
		"path": path,
		"hashes": map[string]string{
			"sha1":   hex.EncodeToString(s1[:]),
			"sha512": hex.EncodeToString(s512[:]),
		},
		"env":       map[string]string{"client": client, "server": server},
		"downloads": []string{link},
		"fileSize":  len(content),
	}
}
