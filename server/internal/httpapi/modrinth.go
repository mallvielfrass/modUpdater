package httpapi

import (
	"net/http"
	"time"
)

func (a *API) importModrinth(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		if tooLarge(err) {
			writeMapped(w, errPayload)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	defer cleanupForm(r)
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	defer file.Close()
	client := &http.Client{Timeout: 3 * time.Minute}
	res, err := a.mods.ImportModrinth(file, client)
	if err != nil {
		writeMapped(w, err)
		return
	}
	if res.Failed == nil {
		res.Failed = []string{}
	}
	writeJSON(w, http.StatusOK, res)
}
