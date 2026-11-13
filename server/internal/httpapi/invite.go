package httpapi

import (
	"html/template"
	"io"
	"net/http"
	"net/url"

	"modupdater/internal/store"
)

var invitePageTmpl = template.Must(template.New("invite").Parse(`<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<title>Регистрация</title>
</head>
<body>
<form method="post" action="/api/auth/register">
<input type="hidden" name="invite_token" value="{{.Token}}">
<label>Имя <input name="username" autocomplete="username"></label>
<label>Пароль <input type="password" name="password" autocomplete="new-password"></label>
<button type="submit">Создать</button>
</form>
</body>
</html>
`))

func (a *API) invitePage(w http.ResponseWriter, token string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	_ = invitePageTmpl.Execute(w, struct{ Token string }{Token: token})
}

func (a *API) listAdmins(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	list, err := a.auth.ListAdmins()
	if err != nil {
		writeMapped(w, err)
		return
	}
	if list == nil {
		list = []store.Admin{}
	}
	writeJSON(w, http.StatusOK, struct {
		Admins []store.Admin `json:"admins"`
	}{Admins: list})
}

func (a *API) deleteAdmin(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	if err := a.auth.DeleteAdmin(p["id"]); err != nil {
		writeMapped(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) createInvite(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	admin, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		TTL *int `json:"ttl_hours"`
	}
	if err := readJSON(w, r, &body); err != nil && err != io.EOF {
		writeMapped(w, err)
		return
	}
	hours := 72
	if body.TTL != nil {
		hours = *body.TTL
	}
	if hours < 1 || hours > 336 {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	tok, exp, err := a.auth.CreateInvite(admin.ID, hours)
	if err != nil {
		writeMapped(w, err)
		return
	}
	link, err := inviteURL(a.cfg.PublicURL, tok)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Invite struct {
			Token     string `json:"token"`
			URL       string `json:"url"`
			ExpiresAt string `json:"expires_at"`
		} `json:"invite"`
	}{Invite: struct {
		Token     string `json:"token"`
		URL       string `json:"url"`
		ExpiresAt string `json:"expires_at"`
	}{Token: tok, URL: link, ExpiresAt: exp}})
}

func inviteURL(publicURL, token string) (string, error) {
	u, err := url.Parse(publicURL)
	if err != nil {
		return "", err
	}
	u.Path = "/invite/" + token
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return u.String(), nil
}

func (a *API) deleteInvite(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	if err := a.auth.DeleteInvite(p["token"]); err != nil {
		writeMapped(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) register(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	host := peerHost(r.RemoteAddr)
	if !a.lim.allow(host) {
		writeError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	form := mediaType(r) == "application/x-www-form-urlencoded"
	token, username, password, err := readRegister(w, r, form)
	if err != nil {
		if form && !errorsIsPayload(err) {
			writeRegisterFail(w)
			return
		}
		writeMapped(w, err)
		return
	}
	res, err := a.auth.Register(token, username, password)
	if err != nil {
		if countsFailure(err) {
			a.lim.fail(host)
		}
		if form {
			writeRegisterFail(w)
			return
		}
		writeMapped(w, err)
		return
	}
	a.lim.success(host)
	if form {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "Аккаунт создан. Войдите в приложении.")
		return
	}
	writeLogin(w, res)
}

func readRegister(w http.ResponseWriter, r *http.Request, form bool) (string, string, string, error) {
	if form {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := r.ParseForm(); err != nil {
			if tooLarge(err) {
				return "", "", "", errPayload
			}
			return "", "", "", errJSON
		}
		return r.PostForm.Get("invite_token"), r.PostForm.Get("username"), r.PostForm.Get("password"), nil
	}
	var body struct {
		InviteToken string `json:"invite_token"`
		Username    string `json:"username"`
		Password    string `json:"password"`
	}
	if err := readJSON(w, r, &body); err != nil {
		if err == io.EOF {
			return "", "", "", errJSON
		}
		return "", "", "", err
	}
	return body.InviteToken, body.Username, body.Password, nil
}

func writeRegisterFail(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = io.WriteString(w, "Ссылка недействительна или имя занято.")
}

func errorsIsPayload(err error) bool {
	return err == errPayload
}
