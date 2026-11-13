package httpapi

import (
	"io"
	"net/http"

	"modupdater/internal/mods"
	"modupdater/internal/store"
)

type modJSON struct {
	store.Mod
	Configs []store.ModConfig `json:"configs"`
}

func (a *API) modJSON(m store.Mod) (modJSON, error) {
	cfgs, err := a.mods.ModConfigs(m.ID)
	if err != nil {
		return modJSON{}, err
	}
	if cfgs == nil {
		cfgs = []store.ModConfig{}
	}
	return modJSON{Mod: m, Configs: cfgs}, nil
}

func (a *API) modsJSON(list []store.Mod) ([]modJSON, error) {
	by, err := a.mods.AllConfigs()
	if err != nil {
		return nil, err
	}
	out := make([]modJSON, 0, len(list))
	for _, m := range list {
		cfgs := by[m.ID]
		if cfgs == nil {
			cfgs = []store.ModConfig{}
		}
		out = append(out, modJSON{Mod: m, Configs: cfgs})
	}
	return out, nil
}

func (a *API) addConfig(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	dest, kind, file, filename, err := readConfigForm(w, r)
	if err != nil {
		writeMapped(w, err)
		return
	}
	defer cleanupForm(r)
	defer file.Close()
	cfg, err := a.mods.AddConfig(p["id"], dest, kind, filename, file)
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Config store.ModConfig `json:"config"`
	}{Config: cfg})
}

func (a *API) deleteConfig(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	if err := a.mods.DeleteConfig(p["id"], p["configId"]); err != nil {
		writeMapped(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readConfigForm(w http.ResponseWriter, r *http.Request) (string, string, io.ReadCloser, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, mods.MaxConfig+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		if tooLarge(err) {
			return "", "", nil, "", errPayload
		}
		return "", "", nil, "", errJSON
	}
	dest, kind := "", ""
	if r.MultipartForm != nil {
		if vs := r.MultipartForm.Value["dest"]; len(vs) > 0 {
			dest = vs[0]
		}
		if vs := r.MultipartForm.Value["kind"]; len(vs) > 0 {
			kind = vs[0]
		}
	}
	if dest == "" || kind == "" {
		return "", "", nil, "", errJSON
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		if tooLarge(err) {
			return "", "", nil, "", errPayload
		}
		return "", "", nil, "", errJSON
	}
	return dest, kind, file, hdr.Filename, nil
}
