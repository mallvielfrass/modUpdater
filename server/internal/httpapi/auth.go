package httpapi

import (
	"net/http"

	"modupdater/internal/auth"
	"modupdater/internal/buildinfo"
	"modupdater/internal/store"
)

type loginResponse struct {
	Token        string      `json:"token"`
	RefreshToken string      `json:"refresh_token"`
	ExpiresAt    string      `json:"expires_at"`
	Admin        store.Admin `json:"admin"`
}

func (a *API) login(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	host := peerHost(r.RemoteAddr)
	if !a.lim.allow(host) {
		writeError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &body); err != nil {
		if err == errPayload {
			writeMapped(w, err)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	res, err := a.auth.Login(body.Username, body.Password)
	if err != nil {
		if countsFailure(err) {
			a.lim.fail(host)
		}
		writeMapped(w, err)
		return
	}
	a.lim.success(host)
	writeLogin(w, res)
}

func (a *API) refresh(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := readJSON(w, r, &body); err != nil {
		if err == errPayload {
			writeMapped(w, err)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	res, err := a.auth.Refresh(body.RefreshToken)
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeLogin(w, res)
}

func writeLogin(w http.ResponseWriter, res auth.Result) {
	writeJSON(w, http.StatusOK, loginResponse{
		Token:        res.Token,
		RefreshToken: res.RefreshToken,
		ExpiresAt:    store.FormatTime(res.ExpiresAt),
		Admin:        res.Admin,
	})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if tok := bearer(r); tok != "" {
		_ = a.auth.Logout(tok)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) me(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	admin, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Admin         store.Admin `json:"admin"`
		ServerVersion string      `json:"server_version"`
	}{Admin: admin, ServerVersion: buildinfo.Version})
}
