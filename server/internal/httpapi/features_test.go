package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"modupdater/internal/store"
)

func TestPendingRestartFromHistory(t *testing.T) {
	e := newEnv(t)
	e.mc.Run = func(string, []string) error { return nil }
	tok := e.token(t)
	e.setMods(t, tok)
	body, ctype := multipartBody(t, "Srv.jar", jarBytes(t, "srv", "1", "Srv", "d"), map[string]string{"client": "true"})
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
	admin := e.adminMods(t, tok)
	if len(admin.PendingRestart) != 1 || admin.PendingRestart[0].Slug != "srv" || admin.PendingRestart[0].Action != "added" || admin.PendingRestart[0].Name != "Srv" {
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
	body, ctype = multipartBody(t, "Only.jar", jarBytes(t, "only", "1", "Only", "d"), map[string]string{"client_only": "true", "client": "false"})
	rec = e.do(http.MethodPost, "/api/admin/mods", tok, ctype, body)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var only struct {
		Mod modJSON `json:"mod"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &only); err != nil {
		t.Fatal(err)
	}
	if !only.Mod.Client || !only.Mod.ClientOnly {
		t.Fatalf("%+v", only.Mod)
	}
	admin = e.adminMods(t, tok)
	if len(admin.PendingRestart) != 1 || admin.PendingRestart[0].Slug != "srv" {
		t.Fatalf("%+v", admin.PendingRestart)
	}
	e.serverLayout(t)
	rec = e.do(http.MethodPost, "/api/admin/server/restart", tok, "application/json", bytes.NewReader([]byte(`{}`)))
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var restarted struct {
		Warned         bool                   `json:"warned"`
		Restarted      bool                   `json:"restarted"`
		LastRestartAt  string                 `json:"last_restart_at"`
		PendingRestart []store.PendingRestart `json:"pending_restart"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &restarted); err != nil {
		t.Fatal(err)
	}
	if !restarted.Warned || !restarted.Restarted || restarted.LastRestartAt == "" || len(restarted.PendingRestart) != 0 {
		t.Fatalf("%+v", restarted)
	}
	time.Sleep(1100 * time.Millisecond)
	body, ctype = multipartBody(t, "Srv.jar", jarBytes(t, "srv", "2", "Srv", "d2"), nil)
	rec = e.do(http.MethodPut, "/api/admin/mods/"+created.Mod.ID, tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	admin = e.adminMods(t, tok)
	if admin.LastRestartAt != restarted.LastRestartAt || len(admin.PendingRestart) != 1 || admin.PendingRestart[0].Action != "updated" || admin.PendingRestart[0].Slug != "srv" {
		t.Fatalf("%s %+v", admin.LastRestartAt, admin.PendingRestart)
	}
	rec = e.do(http.MethodDelete, "/api/admin/mods/"+created.Mod.ID, tok, "", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code, rec.Body.String())
	}
	admin = e.adminMods(t, tok)
	if len(admin.PendingRestart) != 1 || admin.PendingRestart[0].Action != "deleted" || admin.PendingRestart[0].Slug != "srv" {
		t.Fatalf("%+v", admin.PendingRestart)
	}
}

func TestRevokeServerJarPendingAndRestore(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	content := jarBytes(t, "srv", "1", "Srv", "d")
	body, ctype := multipartBody(t, "Srv.jar", content, map[string]string{"client": "true"})
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
	if _, err := os.Stat(filepath.Join(e.modsDir, "Srv.jar")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	stored := filepath.Join(e.data, "revoked", created.Mod.ID+".jar")
	got, err := os.ReadFile(stored)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal(err)
	}
	admin := e.adminMods(t, tok)
	if len(admin.Mods) != 1 || !admin.Mods[0].Revoked || len(admin.PendingRestart) != 1 || admin.PendingRestart[0].Action != "deleted" || admin.PendingRestart[0].Slug != "srv" {
		t.Fatalf("%+v %+v", admin.Mods, admin.PendingRestart)
	}
	pub := e.do(http.MethodGet, "/api/mods", "", "", nil)
	var list struct {
		Mods    []modJSON `json:"mods"`
		Revoked []struct {
			ID string `json:"id"`
		} `json:"revoked"`
	}
	if err := json.Unmarshal(pub.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Mods) != 0 || len(list.Revoked) != 1 || list.Revoked[0].ID != created.Mod.ID {
		t.Fatal(pub.Body.String())
	}
	dl := e.do(http.MethodGet, "/api/mods/"+created.Mod.ID+"/download", "", "", nil)
	if dl.Code != http.StatusNotFound {
		t.Fatal(dl.Code)
	}
	rec = e.do(http.MethodPost, "/api/admin/mods/scan", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	admin = e.adminMods(t, tok)
	if len(admin.Mods) != 1 || !admin.Mods[0].Revoked {
		t.Fatalf("%+v", admin.Mods)
	}
	if _, err := os.Stat(stored); err != nil {
		t.Fatal(err)
	}
	rec = e.do(http.MethodPost, "/api/admin/mods/"+created.Mod.ID+"/restore", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	back, err := os.ReadFile(filepath.Join(e.modsDir, "Srv.jar"))
	if err != nil || !bytes.Equal(back, content) {
		t.Fatal(err)
	}
	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	admin = e.adminMods(t, tok)
	if len(admin.Mods) != 1 || admin.Mods[0].Revoked || len(admin.PendingRestart) != 1 || admin.PendingRestart[0].Action != "added" {
		t.Fatalf("%+v %+v", admin.Mods, admin.PendingRestart)
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
	admin = e.adminMods(t, tok)
	if len(admin.Mods) != 0 || len(admin.PendingRestart) != 1 || admin.PendingRestart[0].Action != "deleted" {
		t.Fatalf("%+v %+v", admin.Mods, admin.PendingRestart)
	}
}

func TestRevokeServerOnly(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	content := jarBytes(t, "side", "1", "Side", "d")
	body, ctype := multipartBody(t, "Side.jar", content, map[string]string{"client": "false"})
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
	if created.Mod.Client || created.Mod.ClientOnly {
		t.Fatalf("%+v", created.Mod)
	}
	rec = e.do(http.MethodPost, "/api/admin/mods/"+created.Mod.ID+"/revoke", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(e.modsDir, "Side.jar")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.data, "revoked", created.Mod.ID+".jar")); err != nil {
		t.Fatal(err)
	}
	admin := e.adminMods(t, tok)
	if len(admin.Mods) != 1 || !admin.Mods[0].Revoked || len(admin.PendingRestart) != 1 || admin.PendingRestart[0].Action != "deleted" {
		t.Fatalf("%+v %+v", admin.Mods, admin.PendingRestart)
	}
	pub := e.do(http.MethodGet, "/api/mods", "", "", nil)
	var list struct {
		Mods    []modJSON `json:"mods"`
		Revoked []struct {
			ID string `json:"id"`
		} `json:"revoked"`
	}
	if err := json.Unmarshal(pub.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Mods) != 0 || len(list.Revoked) != 0 {
		t.Fatal(pub.Body.String())
	}
}

type adminModsBody struct {
	Mods           []modJSON              `json:"mods"`
	LastRestartAt  string                 `json:"last_restart_at"`
	PendingRestart []store.PendingRestart `json:"pending_restart"`
}

func (e *env) adminMods(t *testing.T, tok string) adminModsBody {
	t.Helper()
	rec := e.do(http.MethodGet, "/api/admin/mods", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var body adminModsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestClientOnlyPublicAndConfig(t *testing.T) {
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
	if !created.Mod.Client || !created.Mod.ClientOnly || created.Mod.Configs == nil {
		t.Fatalf("%+v", created.Mod)
	}
	if _, err := os.Stat(filepath.Join(e.data, "client_mods", "Only.jar")); err != nil {
		t.Fatal(err)
	}
	pub := e.do(http.MethodGet, "/api/mods", "", "", nil)
	if pub.Code != http.StatusOK || !bytes.Contains(pub.Body.Bytes(), []byte(`"client_only":true`)) {
		t.Fatal(pub.Body.String())
	}
	file := e.do(http.MethodGet, "/api/mods/"+created.Mod.ID+"/file", "", "", nil)
	if file.Code != http.StatusOK {
		t.Fatal(file.Code, file.Body.String())
	}
	body, ctype = multipartBody(t, "", nil, map[string]string{"client_only": "false", "client": "false"})
	rec = e.do(http.MethodPut, "/api/admin/mods/"+created.Mod.ID, tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var updated struct {
		Mod modJSON `json:"mod"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if !updated.Mod.ClientOnly || !updated.Mod.Client {
		t.Fatalf("%+v", updated.Mod)
	}

	e.setMods(t, tok)
	body, ctype = multipartBody(t, "Srv.jar", jarBytes(t, "srv", "1", "Srv", "d"), map[string]string{"client": "false"})
	rec = e.do(http.MethodPost, "/api/admin/mods", tok, ctype, body)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var hidden struct {
		Mod modJSON `json:"mod"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &hidden); err != nil {
		t.Fatal(err)
	}
	bad, ctype := configBody(t, "../secret", "file", "a.txt", []byte("no"))
	rec = e.do(http.MethodPost, "/api/admin/mods/"+created.Mod.ID+"/configs", tok, ctype, bad)
	if errorCode(t, rec) != "invalid_dest" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	bad, ctype = configBody(t, "config/a.txt", "nope", "a.txt", []byte("no"))
	rec = e.do(http.MethodPost, "/api/admin/mods/"+created.Mod.ID+"/configs", tok, ctype, bad)
	if errorCode(t, rec) != "invalid_json" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	denied, deniedType := configBody(t, "config/a.txt", "file", "a.txt", []byte("no"))
	rec = e.do(http.MethodPost, "/api/admin/mods/"+hidden.Mod.ID+"/configs", tok, deniedType, denied)
	if errorCode(t, rec) != "forbidden" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	payload := bytes.Repeat([]byte("x"), 1500*1024)
	good, ctype := configBody(t, "config/jei/jei.toml", "file", "jei.toml", payload)
	rec = e.do(http.MethodPost, "/api/admin/mods/"+created.Mod.ID+"/configs", tok, ctype, good)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var cfg struct {
		Config store.ModConfig `json:"config"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Config.Dest != "config/jei/jei.toml" || cfg.Config.Kind != "file" || cfg.Config.Filename != "jei.toml" || cfg.Config.Size != int64(len(payload)) {
		t.Fatalf("%+v", cfg.Config)
	}
	blob := filepath.Join(e.data, "client_configs", cfg.Config.ID+".bin")
	if _, err := os.Stat(blob); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	if cfg.Config.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal(cfg.Config.SHA256)
	}
	got := e.do(http.MethodGet, "/api/mods/"+created.Mod.ID+"/configs/"+cfg.Config.ID+"/file", "", "", nil)
	if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), payload) {
		t.Fatal(got.Code, got.Body.Len())
	}
	list := e.do(http.MethodGet, "/api/mods", "", "", nil)
	if !bytes.Contains(list.Body.Bytes(), []byte(cfg.Config.ID)) || !bytes.Contains(list.Body.Bytes(), []byte(`"configs":`)) {
		t.Fatal(list.Body.String())
	}
	admin := e.adminMods(t, tok)
	found := false
	for _, m := range admin.Mods {
		if m.ID == created.Mod.ID && len(m.Configs) == 1 && m.Configs[0].ID == cfg.Config.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", admin.Mods)
	}
	dirBody, dirType := configBody(t, "shaderpacks/ Complementary", "dir", "pack.zip", []byte("PK"))
	rec = e.do(http.MethodPost, "/api/admin/mods/"+created.Mod.ID+"/configs", tok, dirType, dirBody)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var dirCfg struct {
		Config store.ModConfig `json:"config"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dirCfg); err != nil {
		t.Fatal(err)
	}
	rec = e.do(http.MethodDelete, "/api/admin/mods/"+created.Mod.ID+"/configs/"+cfg.Config.ID, tok, "", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(blob); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	rec = e.do(http.MethodDelete, "/api/admin/mods/"+created.Mod.ID, tok, "", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(e.data, "client_configs", dirCfg.Config.ID+".bin")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	left, err := e.db.ListConfigs(created.Mod.ID)
	if err != nil || len(left) != 0 {
		t.Fatal(err, left)
	}
}

func configBody(t *testing.T, dest, kind, name string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("dest", dest); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteField("kind", kind); err != nil {
		t.Fatal(err)
	}
	fw, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func TestReleasesDevAndDelete(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	noAuth := e.do(http.MethodGet, "/api/admin/releases", "", "", nil)
	if noAuth.Code != http.StatusUnauthorized {
		t.Fatal(noAuth.Code, noAuth.Body.String())
	}
	empty := e.do(http.MethodGet, "/api/client/latest", "", "", nil)
	if empty.Code != http.StatusOK {
		t.Fatal(empty.Body.String())
	}
	var latest struct {
		Version       string `json:"version"`
		LinuxSHA256   string `json:"linux_sha256"`
		LinuxSize     int64  `json:"linux_size"`
		WindowsSHA256 string `json:"windows_sha256"`
		WindowsSize   int64  `json:"windows_size"`
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &latest); err != nil {
		t.Fatal(err)
	}
	if latest.Version != "" || latest.LinuxSize != 0 || latest.WindowsSize != 0 || latest.LinuxSHA256 != "" || latest.WindowsSHA256 != "" {
		t.Fatalf("%+v", latest)
	}
	missing, ctype := releaseBody(t, "1.0.0", []byte("linux-a"), nil)
	rec := e.do(http.MethodPost, "/api/admin/releases", tok, ctype, missing)
	if errorCode(t, rec) != "invalid_json" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	bad, ctype := releaseBody(t, "..", []byte("L"), []byte("W"))
	rec = e.do(http.MethodPost, "/api/admin/releases", tok, ctype, bad)
	if errorCode(t, rec) != "invalid_version" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	linuxA := []byte("linux-bin-a")
	winA := []byte("windows-bin-a")
	body, ctype := releaseBody(t, "1.0.0", linuxA, winA)
	rec = e.do(http.MethodPost, "/api/admin/releases", tok, ctype, body)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var uploaded struct {
		Release store.ClientRelease `json:"release"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	if uploaded.Release.Status != "dev" || uploaded.Release.Version != "1.0.0" || uploaded.Release.LinuxSize != int64(len(linuxA)) {
		t.Fatalf("%+v", uploaded.Release)
	}
	latestRec := e.do(http.MethodGet, "/api/client/latest", "", "", nil)
	if err := json.Unmarshal(latestRec.Body.Bytes(), &latest); err != nil {
		t.Fatal(err)
	}
	if latest.Version != "" || latest.LinuxSize != 0 {
		t.Fatalf("%+v", latest)
	}
	pub := e.do(http.MethodGet, "/api/client/releases/1.0.0/linux/file", "", "", nil)
	if pub.Code != http.StatusNotFound {
		t.Fatal(pub.Code, pub.Body.String())
	}
	adminFile := e.do(http.MethodGet, "/api/admin/releases/1.0.0/linux/file", tok, "", nil)
	if adminFile.Code != http.StatusOK || !bytes.Equal(adminFile.Body.Bytes(), linuxA) {
		t.Fatal(adminFile.Code, adminFile.Body.Len())
	}
	if err := os.Remove(filepath.Join(e.data, "releases", "1.0.0", "modupdater-linux")); err != nil {
		t.Fatal(err)
	}
	rec = e.do(http.MethodPost, "/api/admin/releases/1.0.0/release", tok, "", nil)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "conflict" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	body, ctype = releaseBody(t, "1.0.0", linuxA, winA)
	rec = e.do(http.MethodPost, "/api/admin/releases", tok, ctype, body)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodPost, "/api/admin/releases/1.0.0/release", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	var published struct {
		ActiveVersion string              `json:"active_version"`
		Release       store.ClientRelease `json:"release"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &published); err != nil {
		t.Fatal(err)
	}
	if published.ActiveVersion != "1.0.0" || published.Release.Status != "released" {
		t.Fatalf("%+v", published)
	}
	latestRec = e.do(http.MethodGet, "/api/client/latest", "", "", nil)
	if err := json.Unmarshal(latestRec.Body.Bytes(), &latest); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(linuxA)
	if latest.Version != "1.0.0" || latest.LinuxSize != int64(len(linuxA)) || latest.LinuxSHA256 != hex.EncodeToString(sum[:]) || latest.WindowsSize != int64(len(winA)) {
		t.Fatalf("%+v", latest)
	}
	got := e.do(http.MethodGet, "/api/client/releases/1.0.0/windows/file", "", "", nil)
	if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), winA) {
		t.Fatal(got.Code, got.Body.Len())
	}
	body, ctype = releaseBody(t, "1.0.0", linuxA, winA)
	rec = e.do(http.MethodPost, "/api/admin/releases", tok, ctype, body)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "conflict" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	rec = e.do(http.MethodDelete, "/api/admin/releases/1.0.0", tok, "", nil)
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "conflict" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	linuxB := []byte("linux-bin-b")
	winB := []byte("windows-bin-b")
	body, ctype = releaseBody(t, "1.0.1", linuxB, winB)
	rec = e.do(http.MethodPost, "/api/admin/releases", tok, ctype, body)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	latestRec = e.do(http.MethodGet, "/api/client/latest", "", "", nil)
	if err := json.Unmarshal(latestRec.Body.Bytes(), &latest); err != nil {
		t.Fatal(err)
	}
	if latest.Version != "1.0.0" {
		t.Fatalf("%+v", latest)
	}
	if e.do(http.MethodGet, "/api/client/releases/1.0.1/linux/file", "", "", nil).Code != http.StatusNotFound {
		t.Fatal("dev file is public")
	}
	listRec := e.do(http.MethodGet, "/api/admin/releases", tok, "", nil)
	var list struct {
		ActiveVersion string                `json:"active_version"`
		Releases      []store.ClientRelease `json:"releases"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.ActiveVersion != "1.0.0" || len(list.Releases) != 2 {
		t.Fatalf("%+v", list)
	}
	rec = e.do(http.MethodDelete, "/api/admin/releases/1.0.1", tok, "", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(e.data, "releases", "1.0.1")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.data, "releases", "1.0.0", "modupdater-linux")); err != nil {
		t.Fatal(err)
	}
	rec = e.do(http.MethodPost, "/api/admin/releases/1.0.1/release", tok, "", nil)
	if errorCode(t, rec) != "not_found" {
		t.Fatal(rec.Code, rec.Body.String())
	}
}

func releaseBody(t *testing.T, version string, linux, windows []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("version", version); err != nil {
		t.Fatal(err)
	}
	if linux != nil {
		fw, err := w.CreateFormFile("linux_file", "modupdater-linux")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(linux); err != nil {
			t.Fatal(err)
		}
	}
	if windows != nil {
		fw, err := w.CreateFormFile("windows_file", "modupdater-windows.exe")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(windows); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}
