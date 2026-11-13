package httpapi

import (
	"encoding/json"
	"net/http"

	"modupdater/internal/mods"
)

func (a *API) listDatapacks(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	list, err := a.mods.ListDatapacks()
	if err != nil {
		writeMapped(w, err)
		return
	}
	if list == nil {
		list = []mods.Datapack{}
	}
	writeJSON(w, http.StatusOK, struct {
		Datapacks []mods.Datapack `json:"datapacks"`
	}{Datapacks: list})
}

func (a *API) uploadDatapack(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, mods.MaxDatapack+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		if tooLarge(err) {
			writeMapped(w, errPayload)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	defer cleanupForm(r)
	file, hdr, err := r.FormFile("file")
	if err != nil {
		if tooLarge(err) {
			writeMapped(w, errPayload)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	defer file.Close()
	item, err := a.mods.InstallDatapack(hdr.Filename, file)
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Datapack mods.Datapack `json:"datapack"`
	}{Datapack: item})
}

func (a *API) listDatapackFiles(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	files, err := a.mods.ListDatapackFiles(p["name"])
	if err != nil {
		writeMapped(w, err)
		return
	}
	if files == nil {
		files = []string{}
	}
	writeJSON(w, http.StatusOK, struct {
		Files []string `json:"files"`
	}{Files: files})
}

func (a *API) readDatapackFile(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	rel := r.URL.Query().Get("path")
	text, err := a.mods.ReadDatapackFile(p["name"], rel)
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}{Path: rel, Text: text})
}

func (a *API) saveDatapackFile(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, (1<<20)+8192)
	var body struct {
		Path string `json:"path"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if tooLarge(err) {
			writeMapped(w, errPayload)
			return
		}
		writeMapped(w, errJSON)
		return
	}
	if err := a.mods.SaveDatapackFile(p["name"], body.Path, body.Text); err != nil {
		writeMapped(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
