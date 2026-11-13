package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDownloadPage(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	body, ctype := multipartBody(t, "PageMod.jar", jarBytes(t, "pagemod", "1.2.3", "PageMod", "d"), map[string]string{"client_only": "true"})
	rec := e.do(http.MethodPost, "/api/admin/mods", tok, ctype, body)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}

	page := e.do(http.MethodGet, "/", "", "", nil)
	if page.Code != http.StatusOK || page.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatal(page.Code, page.Header().Get("Content-Type"), page.Body.String())
	}
	html := page.Body.String()
	if !strings.Contains(html, "PageMod") || !strings.Contains(html, "PageMod.jar") || !strings.Contains(html, "клиент") || !strings.Contains(html, "добавлен ") {
		t.Fatal(html)
	}
	if strings.Contains(html, "/api/client/releases/") || extraModLinks(html) || !archiveButtons(html) {
		t.Fatal(html)
	}

	devBody, ctype := releaseBody(t, "1.0.1", []byte("linux-dev"), []byte("windows-dev"))
	rec = e.do(http.MethodPost, "/api/admin/releases", tok, ctype, devBody)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	page = e.do(http.MethodGet, "/", "", "", nil)
	if page.Code != http.StatusOK || strings.Contains(page.Body.String(), "/api/client/releases/") {
		t.Fatal(page.Code, page.Body.String())
	}

	relBody, ctype := releaseBody(t, "1.0.0", []byte("linux-bin-a"), []byte("windows-bin-a"))
	rec = e.do(http.MethodPost, "/api/admin/releases", tok, ctype, relBody)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	rec = e.do(http.MethodPost, "/api/admin/releases/1.0.0/release", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	page = e.do(http.MethodGet, "/", "", "", nil)
	html = page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(html, "PageMod") {
		t.Fatal(page.Code, html)
	}
	if !strings.Contains(html, "/api/client/releases/1.0.0/linux/file") || !strings.Contains(html, "/api/client/releases/1.0.0/windows/file") {
		t.Fatal(html)
	}
	if strings.Contains(html, "/api/client/releases/1.0.1/") || extraModLinks(html) || !archiveButtons(html) {
		t.Fatal(html)
	}
}

func TestModsArchive(t *testing.T) {
	e := newEnv(t)
	for _, kind := range []string{"required", "optional", "all"} {
		rec := e.do(http.MethodGet, "/api/mods/archive?kind="+kind, "", "", nil)
		if got := zipFiles(t, rec, "mods-"+kind+".zip"); len(got) != 0 {
			t.Fatal(got)
		}
	}
	for _, path := range []string{"/api/mods/archive", "/api/mods/archive?kind=client", "/api/mods/archive?kind=nope"} {
		bad := e.do(http.MethodGet, path, "", "", nil)
		if bad.Code != http.StatusBadRequest || errorCode(t, bad) != "invalid_limit" {
			t.Fatal(path, bad.Code, bad.Body.String())
		}
	}

	tok := e.token(t)
	e.setMods(t, tok)
	common := jarBytes(t, "common", "1", "Common", "d")
	only := jarBytes(t, "only", "1", "Only", "d")
	addJar(t, e, tok, "Common.jar", common, map[string]string{"client": "true"})
	addJar(t, e, tok, "Only.jar", only, map[string]string{"client_only": "true"})
	addJar(t, e, tok, "Server.jar", jarBytes(t, "srv", "1", "Server", "d"), map[string]string{"client": "false"})
	addJar(t, e, tok, "Gone.jar", jarBytes(t, "gone", "1", "Gone", "d"), map[string]string{"client": "true"})
	if err := os.Remove(filepath.Join(e.modsDir, "Gone.jar")); err != nil {
		t.Fatal(err)
	}
	dead := addJar(t, e, tok, "Dead.jar", jarBytes(t, "dead", "1", "Dead", "d"), map[string]string{"client": "true"})
	rec := e.do(http.MethodDelete, "/api/admin/mods/"+dead, tok, "", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatal(rec.Code, rec.Body.String())
	}

	sameZip(t, zipFiles(t, e.do(http.MethodGet, "/api/mods/archive?kind=required", "", "", nil), "mods-required.zip"), map[string][]byte{"Common.jar": common})
	sameZip(t, zipFiles(t, e.do(http.MethodGet, "/api/mods/archive?kind=optional", "", "", nil), "mods-optional.zip"), map[string][]byte{"Only.jar": only})
	sameZip(t, zipFiles(t, e.do(http.MethodGet, "/api/mods/archive?kind=all", "", "", nil), "mods-all.zip"), map[string][]byte{"Common.jar": common, "Only.jar": only})

	page := e.do(http.MethodGet, "/", "", "", nil)
	if page.Code != http.StatusOK || !archiveButtons(page.Body.String()) || extraModLinks(page.Body.String()) {
		t.Fatal(page.Body.String())
	}
}

func archiveButtons(html string) bool {
	for _, s := range []string{
		`href="/api/mods/archive?kind=required"`,
		`href="/api/mods/archive?kind=optional"`,
		`href="/api/mods/archive?kind=all"`,
		"Архив обязательных клиентских модов",
		"Архив необязательных клиентских модов",
		"Архив всех клиентских модов",
	} {
		if !strings.Contains(html, s) {
			return false
		}
	}
	return true
}

var modDownloadLink = regexp.MustCompile(`/api/mods/[0-9a-f]{32}/download`)

func extraModLinks(html string) bool {
	for _, kind := range []string{"required", "optional", "all"} {
		html = strings.ReplaceAll(html, "/api/mods/archive?kind="+kind, "")
	}
	html = modDownloadLink.ReplaceAllString(html, "")
	return strings.Contains(html, "/api/mods/")
}

func TestModDownload(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	serverJar := jarBytes(t, "srv", "1", "ServerMod", "d")
	onlyJar := jarBytes(t, "only", "1", "OnlyMod", "d")
	bothJar := jarBytes(t, "both", "1", "BothMod", "d")
	serverID := addJar(t, e, tok, "ServerMod.jar", serverJar, map[string]string{"client": "false"})
	onlyID := addJar(t, e, tok, "OnlyMod.jar", onlyJar, map[string]string{"client_only": "true"})
	bothID := addJar(t, e, tok, "BothMod.jar", bothJar, map[string]string{"client": "true"})

	assertJar(t, e, serverID, "ServerMod.jar", serverJar)
	assertJar(t, e, onlyID, "OnlyMod.jar", onlyJar)
	assertJar(t, e, bothID, "BothMod.jar", bothJar)

	page := e.do(http.MethodGet, "/", "", "", nil)
	if page.Code != http.StatusOK {
		t.Fatal(page.Code, page.Body.String())
	}
	html := page.Body.String()
	both := strings.Index(html, `<a class="mod-link" href="/mods/`+bothID+`">BothMod</a>`)
	only := strings.Index(html, `<a class="mod-link" href="/mods/`+onlyID+`">OnlyMod</a>`)
	srv := strings.Index(html, `<a class="mod-link" href="/mods/`+serverID+`">ServerMod</a>`)
	if both < 0 || only < 0 || srv < 0 || both > only || only > srv || !strings.Contains(html, "добавлен ") || !archiveButtons(html) {
		t.Fatal(html)
	}
	for _, id := range []string{bothID, onlyID, serverID} {
		if strings.Count(html, `/api/mods/`+id+`/download`) != 1 || !strings.Contains(html, `<a class="dl" href="/api/mods/`+id+`/download"`) {
			t.Fatal(html)
		}
	}

	if err := os.Remove(filepath.Join(e.modsDir, "ServerMod.jar")); err != nil {
		t.Fatal(err)
	}
	missing := e.do(http.MethodGet, "/api/mods/"+serverID+"/download", "", "", nil)
	if missing.Code != http.StatusNotFound || errorCode(t, missing) != "not_found" {
		t.Fatal(missing.Code, missing.Body.String())
	}
}

func TestModKind(t *testing.T) {
	cases := []struct {
		client, only, required bool
		want                   string
	}{
		{true, true, false, "необязательный"},
		{true, true, true, "обязательный"},
		{true, false, false, "обязательный"},
		{true, false, true, "обязательный"},
		{false, false, false, "серверный мод"},
		{false, false, true, "обязательный"},
	}
	for _, c := range cases {
		if got := modKind(c.client, c.only, c.required); got != c.want {
			t.Fatalf("client=%v only=%v required=%v got %q", c.client, c.only, c.required, got)
		}
	}
}

func TestModPage(t *testing.T) {
	e := newEnv(t)
	tok := e.token(t)
	e.setMods(t, tok)
	onlyID := addJar(t, e, tok, "OnlyMod.jar", jarBytes(t, "only", "1.2.3", "OnlyMod", "optional text"), map[string]string{"client_only": "true", "client_required": "false"})
	reqID := addJar(t, e, tok, "ReqMod.jar", jarBytes(t, "req", "2", "ReqMod", "required text"), map[string]string{"client_only": "true", "client_required": "true"})
	clientID := addJar(t, e, tok, "ClientMod.jar", jarBytes(t, "clientmod", "3", "ClientMod", "client text"), map[string]string{"client": "true"})
	srvID := addJar(t, e, tok, "ServerMod.jar", jarBytes(t, "srv", "9", "ServerMod", "server text"), map[string]string{"client": "false"})
	goneID := addJar(t, e, tok, "GoneMod.jar", jarBytes(t, "gone", "1", "GoneMod", "gone text"), map[string]string{"client": "false"})

	only := e.do(http.MethodGet, "/mods/"+onlyID, "", "", nil)
	onlyHTML := only.Body.String()
	if only.Code != http.StatusOK || only.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatal(only.Code, only.Header().Get("Content-Type"), onlyHTML)
	}
	if !modPageOK(onlyHTML, onlyID, "OnlyMod", "optional text", "OnlyMod.jar", "1.2.3", "необязательный") || strings.Contains(onlyHTML, "серверный мод") || strings.Contains(onlyHTML, ">обязательный<") {
		t.Fatal(onlyHTML)
	}
	req := e.do(http.MethodGet, "/mods/"+reqID, "", "", nil)
	reqHTML := req.Body.String()
	if req.Code != http.StatusOK || !modPageOK(reqHTML, reqID, "ReqMod", "required text", "ReqMod.jar", "2", "обязательный") || strings.Contains(reqHTML, "необязательный") || strings.Contains(reqHTML, "серверный мод") {
		t.Fatal(req.Code, reqHTML)
	}
	clientPage := e.do(http.MethodGet, "/mods/"+clientID, "", "", nil)
	clientHTML := clientPage.Body.String()
	if clientPage.Code != http.StatusOK || !modPageOK(clientHTML, clientID, "ClientMod", "client text", "ClientMod.jar", "3", "обязательный") || strings.Contains(clientHTML, "необязательный") || strings.Contains(clientHTML, "серверный мод") {
		t.Fatal(clientPage.Code, clientHTML)
	}
	srv := e.do(http.MethodGet, "/mods/"+srvID, "", "", nil)
	srvHTML := srv.Body.String()
	if srv.Code != http.StatusOK || !modPageOK(srvHTML, srvID, "ServerMod", "server text", "ServerMod.jar", "9", "серверный мод") || strings.Contains(srvHTML, "необязательный") || strings.Contains(srvHTML, "обязательный") {
		t.Fatal(srv.Code, srvHTML)
	}

	list := e.do(http.MethodGet, "/", "", "", nil)
	listHTML := list.Body.String()
	if list.Code != http.StatusOK {
		t.Fatal(list.Code, listHTML)
	}
	onlyCard := modCard(listHTML, onlyID)
	reqCard := modCard(listHTML, reqID)
	clientCard := modCard(listHTML, clientID)
	srvCard := modCard(listHTML, srvID)
	if !strings.Contains(onlyCard, `<a class="mod-link" href="/mods/`+onlyID+`">OnlyMod</a>`) || !strings.Contains(onlyCard, `<a class="dl" href="/api/mods/`+onlyID+`/download"`) || !strings.Contains(onlyCard, ">клиент<") || !strings.Contains(onlyCard, ">необязательный<") || strings.Contains(onlyCard, ">обязательный<") {
		t.Fatal(onlyCard)
	}
	if !strings.Contains(reqCard, `<a class="mod-link" href="/mods/`+reqID+`">ReqMod</a>`) || !strings.Contains(reqCard, `<a class="dl" href="/api/mods/`+reqID+`/download"`) || !strings.Contains(reqCard, ">клиент<") || !strings.Contains(reqCard, ">обязательный<") || strings.Contains(reqCard, "необязательный") {
		t.Fatal(reqCard)
	}
	if !strings.Contains(clientCard, `<a class="mod-link" href="/mods/`+clientID+`">ClientMod</a>`) || !strings.Contains(clientCard, `<a class="dl" href="/api/mods/`+clientID+`/download"`) || !strings.Contains(clientCard, ">обязательный<") || strings.Contains(clientCard, "необязательный") || strings.Contains(clientCard, ">клиент<") {
		t.Fatal(clientCard)
	}
	if !strings.Contains(srvCard, `<a class="mod-link" href="/mods/`+srvID+`">ServerMod</a>`) || !strings.Contains(srvCard, `<a class="dl" href="/api/mods/`+srvID+`/download"`) || strings.Contains(srvCard, "необязательный") || strings.Contains(srvCard, "обязательный") || strings.Contains(srvCard, ">клиент<") {
		t.Fatal(srvCard)
	}
	assertClientRequiredJSON(t, e, onlyID, false)
	assertClientRequiredJSON(t, e, reqID, true)
	assertClientRequiredJSON(t, e, clientID, false)

	unknown := e.do(http.MethodGet, "/mods/0123456789abcdef0123456789abcdef", "", "", nil)
	if unknown.Code != http.StatusNotFound || errorCode(t, unknown) != "not_found" {
		t.Fatal(unknown.Code, unknown.Body.String())
	}
	rec := e.do(http.MethodPost, "/api/admin/mods/"+goneID+"/revoke", tok, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	revoked := e.do(http.MethodGet, "/mods/"+goneID, "", "", nil)
	if revoked.Code != unknown.Code || revoked.Body.String() != unknown.Body.String() || errorCode(t, revoked) != "not_found" {
		t.Fatal(revoked.Code, revoked.Body.String())
	}
	list = e.do(http.MethodGet, "/", "", "", nil)
	if strings.Contains(list.Body.String(), "GoneMod") || strings.Contains(list.Body.String(), goneID) {
		t.Fatal(list.Body.String())
	}
	dl := e.do(http.MethodGet, "/api/mods/"+srvID+"/download", "", "", nil)
	if dl.Code != http.StatusOK || dl.Header().Get("Content-Disposition") != `attachment; filename="ServerMod.jar"` {
		t.Fatal(dl.Code, dl.Header().Get("Content-Disposition"))
	}
}

func modCard(html, id string) string {
	start := strings.Index(html, `<a class="mod-link" href="/mods/`+id+`">`)
	if start < 0 {
		return ""
	}
	rest := html[start:]
	end := strings.Index(rest, `</article>`)
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func assertClientRequiredJSON(t *testing.T, e *env, id string, required bool) {
	t.Helper()
	one := e.do(http.MethodGet, "/api/mods/"+id, "", "", nil)
	if one.Code != http.StatusOK || !strings.Contains(one.Body.String(), `"client_required":`+boolJSON(required)) {
		t.Fatal(one.Code, one.Body.String())
	}
	list := e.do(http.MethodGet, "/api/mods", "", "", nil)
	if list.Code != http.StatusOK {
		t.Fatal(list.Code, list.Body.String())
	}
	var body struct {
		Mods []struct {
			ID             string `json:"id"`
			ClientRequired bool   `json:"client_required"`
		} `json:"mods"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, m := range body.Mods {
		if m.ID == id {
			if m.ClientRequired != required {
				t.Fatalf("id %s client_required %v", id, m.ClientRequired)
			}
			return
		}
	}
	t.Fatalf("missing %s in %s", id, list.Body.String())
}

func boolJSON(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func modPageOK(html, id, name, desc, filename, version, kind string) bool {
	for _, s := range []string{
		name, desc, filename, version, kind, "добавлен ",
		`<a class="dl" href="/api/mods/` + id + `/download"`,
		`stroke="currentColor"`,
		"#3dd68c",
		"#14161c",
	} {
		if !strings.Contains(html, s) {
			return false
		}
	}
	return !strings.Contains(html, `<a class="mod-link"`)
}

func assertJar(t *testing.T, e *env, id, filename string, content []byte) {
	t.Helper()
	rec := e.do(http.MethodGet, "/api/mods/"+id+"/download", "", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatal(rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Content-Disposition") != `attachment; filename="`+filename+`"` {
		t.Fatal(rec.Header().Get("Content-Disposition"))
	}
	if !bytes.Equal(rec.Body.Bytes(), content) {
		t.Fatal("bytes mismatch")
	}
}

func addJar(t *testing.T, e *env, tok, filename string, content []byte, fields map[string]string) string {
	t.Helper()
	body, ctype := multipartBody(t, filename, content, fields)
	rec := e.do(http.MethodPost, "/api/admin/mods", tok, ctype, body)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	var created struct {
		Mod modJSON `json:"mod"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Mod.ID == "" {
		t.Fatal(rec.Body.String())
	}
	return created.Mod.ID
}

func zipFiles(t *testing.T, rec *httptest.ResponseRecorder, filename string) map[string][]byte {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatal(rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Content-Disposition") != `attachment; filename="`+filename+`"` {
		t.Fatal(rec.Header().Get("Content-Disposition"))
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		if f.Name != filepath.Base(f.Name) || strings.ContainsAny(f.Name, `/\`) {
			t.Fatalf("entry %q", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = b
	}
	return out
}

func sameZip(t *testing.T, got, want map[string][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for name, b := range want {
		if !bytes.Equal(got[name], b) {
			t.Fatalf("mismatch %s", name)
		}
	}
}
