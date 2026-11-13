package httpapi

import (
	"net"
	"net/http"
	"strings"
	"time"

	"modupdater/internal/auth"
	"modupdater/internal/config"
	"modupdater/internal/minecraft"
	"modupdater/internal/mods"
	"modupdater/internal/store"
)

type API struct {
	cfg    *config.Config
	auth   *auth.Service
	mods   *mods.Service
	mc     *minecraft.Service
	lim    *limiter
	routes []route
}

type route struct {
	method string
	segs   []segment
	fn     func(http.ResponseWriter, *http.Request, map[string]string)
}

type segment struct {
	lit   string
	param string
}

func New(cfg *config.Config, au *auth.Service, md *mods.Service, mc *minecraft.Service) *API {
	if md != nil && mc != nil {
		md.SetServiceStart(mc.ActiveEnter)
		md.SetWorldHold(mc.PrepareWorldCopy)
	}
	a := &API{
		cfg:  cfg,
		auth: au,
		mods: md,
		mc:   mc,
		lim:  &limiter{hits: map[string][]time.Time{}},
	}
	a.handle(http.MethodPost, "/api/auth/login", a.login)
	a.handle(http.MethodPost, "/api/auth/refresh", a.refresh)
	a.handle(http.MethodPost, "/api/auth/logout", a.logout)
	a.handle(http.MethodGet, "/api/auth/me", a.me)
	a.handle(http.MethodPost, "/api/auth/register", a.register)
	a.handle(http.MethodGet, "/api/mods", a.publicMods)
	a.handle(http.MethodGet, "/api/mods/archive", a.publicModsArchive)
	a.handle(http.MethodGet, "/api/mods/{id}", a.publicMod)
	a.handle(http.MethodGet, "/api/mods/{id}/file", a.publicFile)
	a.handle(http.MethodGet, "/api/mods/{id}/download", a.publicModDownload)
	a.handle(http.MethodGet, "/api/mods/{id}/configs/{configId}/file", a.publicConfigFile)
	a.handle(http.MethodGet, "/api/history", a.publicHistory)
	a.handle(http.MethodGet, "/api/history/{id}/file", a.publicHistoryFile)
	a.handle(http.MethodGet, "/", a.downloadPage)
	a.handle(http.MethodGet, "/changelog", a.changelogPage)
	a.handle(http.MethodGet, "/mods/{id}", a.modPage)
	a.handle(http.MethodGet, "/api/client/latest", a.clientLatest)
	a.handle(http.MethodGet, "/api/client/releases/{version}/{os}/file", a.publicReleaseFile)
	a.handle(http.MethodGet, "/api/admin/admins", a.listAdmins)
	a.handle(http.MethodDelete, "/api/admin/admins/{id}", a.deleteAdmin)
	a.handle(http.MethodPost, "/api/admin/invites", a.createInvite)
	a.handle(http.MethodDelete, "/api/admin/invites/{token}", a.deleteInvite)
	a.handle(http.MethodGet, "/api/admin/settings", a.getSettings)
	a.handle(http.MethodPut, "/api/admin/settings", a.putSettings)
	a.handle(http.MethodPost, "/api/admin/mods/scan", a.scanMods)
	a.handle(http.MethodPost, "/api/admin/mods/modrinth", a.importModrinth)
	a.handle(http.MethodGet, "/api/admin/mods", a.adminMods)
	a.handle(http.MethodPost, "/api/admin/mods", a.addMod)
	a.handle(http.MethodPut, "/api/admin/mods/{id}", a.updateMod)
	a.handle(http.MethodDelete, "/api/admin/mods/{id}", a.deleteMod)
	a.handle(http.MethodPost, "/api/admin/mods/{id}/revoke", a.revokeMod)
	a.handle(http.MethodPost, "/api/admin/mods/{id}/restore", a.restoreMod)
	a.handle(http.MethodPost, "/api/admin/mods/{id}/configs", a.addConfig)
	a.handle(http.MethodDelete, "/api/admin/mods/{id}/configs/{configId}", a.deleteConfig)
	a.handle(http.MethodGet, "/api/admin/releases", a.listReleases)
	a.handle(http.MethodPost, "/api/admin/releases", a.uploadRelease)
	a.handle(http.MethodGet, "/api/admin/releases/{version}/{os}/file", a.adminReleaseFile)
	a.handle(http.MethodPost, "/api/admin/releases/{version}/release", a.publishRelease)
	a.handle(http.MethodDelete, "/api/admin/releases/{version}", a.deleteRelease)
	a.handle(http.MethodGet, "/api/admin/changelog", a.listChangelog)
	a.handle(http.MethodPost, "/api/admin/changelog", a.generateChangelog)
	a.handle(http.MethodGet, "/api/admin/backups", a.listBackups)
	a.handle(http.MethodPut, "/api/admin/backups/time", a.putBackupTime)
	a.handle(http.MethodPut, "/api/admin/backups/scope", a.putBackupScope)
	a.handle(http.MethodPost, "/api/admin/backups", a.createBackup)
	a.handle(http.MethodPost, "/api/admin/backups/milestone", a.createMilestone)
	a.handle(http.MethodPut, "/api/admin/backups/s3", a.putS3)
	a.handle(http.MethodPut, "/api/admin/backups/dest", a.putBackupDest)
	a.handle(http.MethodPost, "/api/admin/backups/s3/check", a.checkS3)
	a.handle(http.MethodPost, "/api/admin/backups/world", a.uploadWorld)
	a.handle(http.MethodGet, "/api/admin/backups/{id}/file", a.downloadBackup)
	a.handle(http.MethodPost, "/api/admin/backups/{id}/rollback", a.rollbackBackup)
	a.handle(http.MethodPost, "/api/admin/backups/{id}/launch", a.launchBackup)
	a.handle(http.MethodDelete, "/api/admin/backups/{id}", a.deleteBackup)
	a.handle(http.MethodPost, "/api/admin/server/restart", a.restart)
	a.handle(http.MethodGet, "/api/admin/server/rcon", a.rconStatus)
	a.handle(http.MethodGet, "/api/admin/server/logs", a.logs)
	a.handle(http.MethodGet, "/api/admin/datapacks", a.listDatapacks)
	a.handle(http.MethodPost, "/api/admin/datapacks", a.uploadDatapack)
	a.handle(http.MethodGet, "/api/admin/datapacks/{name}/files", a.listDatapackFiles)
	a.handle(http.MethodGet, "/api/admin/datapacks/{name}/file", a.readDatapackFile)
	a.handle(http.MethodPut, "/api/admin/datapacks/{name}/file", a.saveDatapackFile)
	return a
}

func (a *API) Listen(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           a,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	return srv.ListenAndServe()
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodGet && (r.URL.Path == "/invite" || strings.HasPrefix(r.URL.Path, "/invite/")) {
		token := ""
		if strings.HasPrefix(r.URL.Path, "/invite/") {
			token = strings.TrimPrefix(r.URL.Path, "/invite/")
		}
		a.invitePage(w, token)
		return
	}
	fn, params, ok := a.match(r.Method, r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	fn(w, r, params)
}

func (a *API) handle(method, pattern string, fn func(http.ResponseWriter, *http.Request, map[string]string)) {
	a.routes = append(a.routes, route{method: method, segs: parsePattern(pattern), fn: fn})
}

func parsePattern(pattern string) []segment {
	pattern = strings.Trim(pattern, "/")
	if pattern == "" {
		return nil
	}
	var out []segment
	for _, part := range strings.Split(pattern, "/") {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") && len(part) > 2 {
			out = append(out, segment{param: part[1 : len(part)-1]})
			continue
		}
		out = append(out, segment{lit: part})
	}
	return out
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func (a *API) match(method, path string) (func(http.ResponseWriter, *http.Request, map[string]string), map[string]string, bool) {
	parts := splitPath(path)
	var best func(http.ResponseWriter, *http.Request, map[string]string)
	var bestParams map[string]string
	bestN := int(^uint(0) >> 1)
	found := false
	for i := range a.routes {
		rt := &a.routes[i]
		if rt.method != method || len(rt.segs) != len(parts) {
			continue
		}
		params := map[string]string{}
		ok := true
		n := 0
		for i, seg := range rt.segs {
			if seg.param != "" {
				params[seg.param] = parts[i]
				n++
				continue
			}
			if seg.lit != parts[i] {
				ok = false
				break
			}
		}
		if !ok || n >= bestN {
			continue
		}
		best = rt.fn
		bestParams = params
		bestN = n
		found = true
	}
	return best, bestParams, found
}

func (a *API) requireAdmin(w http.ResponseWriter, r *http.Request) (store.Admin, bool) {
	tok := bearer(r)
	if tok == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return store.Admin{}, false
	}
	admin, err := a.auth.AdminFromToken(tok)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return store.Admin{}, false
	}
	return admin, true
}

func bearer(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	tok := strings.TrimSpace(h[len(prefix):])
	if !store.ValidToken(tok) {
		return ""
	}
	return tok
}

func peerHost(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return host
}
