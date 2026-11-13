package httpapi

import (
	"io"
	"net/http"

	"modupdater/internal/mods"
	"modupdater/internal/store"
)

type clientLatest struct {
	Version       string `json:"version"`
	LinuxSHA256   string `json:"linux_sha256"`
	LinuxSize     int64  `json:"linux_size"`
	WindowsSHA256 string `json:"windows_sha256"`
	WindowsSize   int64  `json:"windows_size"`
}

func (a *API) clientLatest(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	rel, ok, err := a.mods.Latest()
	if err != nil {
		writeMapped(w, err)
		return
	}
	body := clientLatest{}
	if ok {
		body = clientLatest{
			Version: rel.Version, LinuxSHA256: rel.LinuxSHA256, LinuxSize: rel.LinuxSize,
			WindowsSHA256: rel.WindowsSHA256, WindowsSize: rel.WindowsSize,
		}
	}
	writeJSON(w, http.StatusOK, body)
}

func (a *API) publicReleaseFile(w http.ResponseWriter, r *http.Request, p map[string]string) {
	f, name, err := a.mods.OpenReleaseFile(p["version"], p["os"], true)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	defer f.Close()
	serveFile(w, f, name)
}

func (a *API) listReleases(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	active, list, err := a.mods.ListReleases()
	if err != nil {
		writeMapped(w, err)
		return
	}
	if list == nil {
		list = []store.ClientRelease{}
	}
	writeJSON(w, http.StatusOK, struct {
		ActiveVersion string                `json:"active_version"`
		Releases      []store.ClientRelease `json:"releases"`
	}{ActiveVersion: active, Releases: list})
}

func (a *API) uploadRelease(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	version, linux, windows, err := readReleaseForm(w, r)
	if err != nil {
		writeMapped(w, err)
		return
	}
	defer cleanupForm(r)
	defer linux.Close()
	defer windows.Close()
	rel, created, err := a.mods.UploadRelease(version, linux, windows)
	if err != nil {
		writeMapped(w, err)
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	writeJSON(w, code, struct {
		Release store.ClientRelease `json:"release"`
	}{Release: rel})
}

func (a *API) adminReleaseFile(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	f, name, err := a.mods.OpenReleaseFile(p["version"], p["os"], false)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	defer f.Close()
	serveFile(w, f, name)
}

func (a *API) publishRelease(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	rel, err := a.mods.PublishRelease(p["version"])
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		ActiveVersion string              `json:"active_version"`
		Release       store.ClientRelease `json:"release"`
	}{ActiveVersion: rel.Version, Release: rel})
}

func (a *API) deleteRelease(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	if err := a.mods.DeleteRelease(p["version"]); err != nil {
		writeMapped(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readReleaseForm(w http.ResponseWriter, r *http.Request) (string, io.ReadCloser, io.ReadCloser, error) {
	r.Body = http.MaxBytesReader(w, r.Body, mods.MaxRelease*2+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		if tooLarge(err) {
			return "", nil, nil, errPayload
		}
		return "", nil, nil, errJSON
	}
	version := ""
	if r.MultipartForm != nil {
		if vs := r.MultipartForm.Value["version"]; len(vs) > 0 {
			version = vs[0]
		}
	}
	if version == "" {
		return "", nil, nil, errJSON
	}
	linux, _, err := r.FormFile("linux_file")
	if err != nil {
		if tooLarge(err) {
			return "", nil, nil, errPayload
		}
		return "", nil, nil, errJSON
	}
	windows, _, err := r.FormFile("windows_file")
	if err != nil {
		linux.Close()
		if tooLarge(err) {
			return "", nil, nil, errPayload
		}
		return "", nil, nil, errJSON
	}
	return version, linux, windows, nil
}
