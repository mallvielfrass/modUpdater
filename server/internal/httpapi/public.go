package httpapi

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"modupdater/internal/mods"
	"modupdater/internal/store"
)

func (a *API) publicMods(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	list, err := a.mods.PublicList()
	if err != nil {
		writeMapped(w, err)
		return
	}
	if list == nil {
		list = []store.Mod{}
	}
	views, err := a.modsJSON(list)
	if err != nil {
		writeMapped(w, err)
		return
	}
	revoked, err := a.mods.PublicRevoked()
	if err != nil {
		writeMapped(w, err)
		return
	}
	if revoked == nil {
		revoked = []store.Mod{}
	}
	writeJSON(w, http.StatusOK, struct {
		Mods    []modJSON    `json:"mods"`
		Revoked []revokedMod `json:"revoked"`
	}{Mods: views, Revoked: revokedMods(revoked)})
}

type revokedMod struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	Filename       string `json:"filename"`
	SHA256         string `json:"sha256"`
	ClientOnly     bool   `json:"client_only"`
	ClientRequired bool   `json:"client_required"`
}

func revokedMods(list []store.Mod) []revokedMod {
	out := make([]revokedMod, 0, len(list))
	for _, m := range list {
		out = append(out, revokedMod{ID: m.ID, Slug: m.Slug, Name: m.Name, Filename: m.Filename, SHA256: m.SHA256, ClientOnly: m.ClientOnly, ClientRequired: m.ClientRequired})
	}
	return out
}

func (a *API) publicMod(w http.ResponseWriter, r *http.Request, p map[string]string) {
	m, err := a.mods.PublicGet(p["id"])
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	view, err := a.modJSON(m)
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Mod modJSON `json:"mod"`
	}{Mod: view})
}

func (a *API) publicFile(w http.ResponseWriter, r *http.Request, p map[string]string) {
	f, name, err := a.mods.OpenClientFile(p["id"])
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	defer f.Close()
	serveFile(w, f, name)
}

func (a *API) publicModDownload(w http.ResponseWriter, r *http.Request, p map[string]string) {
	f, name, err := a.mods.OpenDownloadFile(p["id"])
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	defer f.Close()
	serveFile(w, f, name)
}

func (a *API) publicModsArchive(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	filename, keep, ok := archiveSpec(r.URL.Query().Get("kind"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_limit")
		return
	}
	list, err := a.mods.PublicList()
	if err != nil {
		writeMapped(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.WriteHeader(http.StatusOK)
	zw := zip.NewWriter(w)
	seen := map[string]struct{}{}
	for _, m := range list {
		if keep(m) {
			a.addArchiveEntry(zw, m.ID, seen)
		}
	}
	_ = zw.Close()
}

func archiveSpec(kind string) (string, func(store.Mod) bool, bool) {
	switch kind {
	case "required":
		return "mods-required.zip", func(m store.Mod) bool { return m.Client && !m.ClientOnly }, true
	case "optional":
		return "mods-optional.zip", func(m store.Mod) bool { return m.ClientOnly }, true
	case "all":
		return "mods-all.zip", func(m store.Mod) bool { return m.Client }, true
	default:
		return "", nil, false
	}
}

func (a *API) addArchiveEntry(zw *zip.Writer, id string, seen map[string]struct{}) {
	f, name, err := a.mods.OpenClientFile(id)
	if err != nil {
		return
	}
	defer f.Close()
	safe, err := mods.SafeJarName(name)
	if err != nil || safe != name || strings.ContainsAny(safe, `/\`) {
		return
	}
	if _, ok := seen[safe]; ok {
		return
	}
	w, err := zw.Create(safe)
	if err != nil {
		return
	}
	if _, err := io.Copy(w, f); err != nil {
		return
	}
	seen[safe] = struct{}{}
}

func (a *API) publicConfigFile(w http.ResponseWriter, r *http.Request, p map[string]string) {
	f, name, err := a.mods.OpenConfigFile(p["id"], p["configId"])
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	defer f.Close()
	serveFile(w, f, name)
}

func (a *API) publicHistory(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	limit, err := boundedQuery(r, "limit", 50, 1, 200)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit")
		return
	}
	events, err := a.mods.PublicHistory(limit)
	if err != nil {
		writeMapped(w, err)
		return
	}
	if events == nil {
		events = []mods.Event{}
	}
	writeJSON(w, http.StatusOK, struct {
		Events []mods.Event `json:"events"`
	}{Events: events})
}

func (a *API) publicHistoryFile(w http.ResponseWriter, r *http.Request, p map[string]string) {
	f, name, err := a.mods.OpenHistoryFile(p["id"])
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	defer f.Close()
	serveFile(w, f, name)
}

func serveFile(w http.ResponseWriter, f *os.File, name string) {
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", name))
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	_, _ = io.Copy(w, f)
}

func boundedQuery(r *http.Request, key string, def, min, max int) (int, error) {
	q := r.URL.Query()
	if !q.Has(key) {
		return def, nil
	}
	n, err := strconv.Atoi(q.Get(key))
	if err != nil || n < min || n > max {
		return 0, errLimit
	}
	return n, nil
}
