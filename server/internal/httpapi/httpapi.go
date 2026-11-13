package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"modupdater/internal/auth"
	"modupdater/internal/config"
	"modupdater/internal/minecraft"
	"modupdater/internal/mods"
	"modupdater/internal/store"
)

var allowedCodes = map[string]struct{}{
	"invalid_json": {}, "invalid_name": {}, "invalid_password": {}, "invalid_filename": {},
	"invalid_mods_dir": {}, "invalid_config": {}, "invalid_limit": {}, "unauthorized": {},
	"forbidden": {}, "not_found": {}, "conflict": {}, "invite_invalid": {},
	"username_taken": {}, "mods_dir_unset": {}, "slug_exists": {}, "restart_in_progress": {},
	"rcon_failed": {}, "restart_failed": {}, "logs_failed": {}, "rate_limited": {},
	"payload_too_large": {}, "internal": {}, "invalid_dest": {}, "invalid_version": {},
	"backup_busy": {}, "backup_failed": {}, "rollback_failed": {},
	"Нет доступа к каталогу сервера.": {}, "Не удалось скопировать файлы сервера.": {},
	"Некорректное время бекапа.": {}, "Некорректная область бекапа.": {},
	"В архиве нет level.dat.": {}, "Архив мира повреждён.": {},
	"В архиве нет pack.mcmeta.": {}, "Архив датапака повреждён.": {},
	"Некорректное имя датапака.": {}, "Не удалось записать датапак.": {},
	"Не удалось прочитать server.properties.": {},
	"Некорректный путь файла.":                {}, "Файл не является текстом.": {},
	"Файл не найден.": {}, "Датапак не найден.": {}, "Слишком большой файл.": {},
	"Бекап не найден.":                    {},
	"Сначала создайте веху.":              {},
	"Бекап уже выполняется.":              {},
	"Сначала удалите снапшоты этой вехи.": {},
	"Нет доступа к бакету.":               {},
	"Укажите бакет и ключи.":              {},
	"Не удалось прочитать файл мода.":     {},
}

var (
	errJSON    = errors.New("invalid_json")
	errPayload = errors.New("payload_too_large")
	errLimit   = errors.New("invalid_limit")
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

func codeAllowed(code string) bool {
	if _, ok := allowedCodes[code]; ok {
		return true
	}
	if len(code) > 400 || strings.ContainsAny(code, "\r\n") {
		return false
	}
	prefixes := []string{
		"Нет доступа к каталогу датапаков ",
		"Нет доступа к каталогу ",
		"Нет доступа к файлу ",
		"Не удалось записать файл ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(code, prefix) && strings.HasSuffix(code, ".") && len(code) > len(prefix)+len(".") {
			return true
		}
	}
	return false
}

func writeError(w http.ResponseWriter, status int, code string) {
	if !codeAllowed(code) {
		status = http.StatusInternalServerError
		code = "internal"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{Error: code})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeMapped(w http.ResponseWriter, err error) {
	var backupAccess *mods.BackupAccessError
	if errors.As(err, &backupAccess) && backupAccess != nil && strings.TrimSpace(backupAccess.Path) != "" {
		writeError(w, http.StatusBadGateway, backupAccess.Error())
		return
	}
	var modAccess *mods.ModAccessError
	if errors.As(err, &modAccess) && modAccess != nil && strings.TrimSpace(modAccess.Path) != "" {
		writeError(w, http.StatusBadGateway, modAccess.Error())
		return
	}
	var modWrite *mods.ModWriteError
	if errors.As(err, &modWrite) && modWrite != nil && strings.TrimSpace(modWrite.Path) != "" {
		writeError(w, http.StatusBadGateway, modWrite.Error())
		return
	}
	var access *mods.DatapackAccessError
	if errors.As(err, &access) && access != nil {
		writeError(w, http.StatusBadGateway, access.Error())
		return
	}
	switch {
	case errors.Is(err, auth.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized")
	case errors.Is(err, auth.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "invalid_name")
	case errors.Is(err, auth.ErrInvalidPassword):
		writeError(w, http.StatusBadRequest, "invalid_password")
	case errors.Is(err, auth.ErrInviteInvalid):
		writeError(w, http.StatusBadRequest, "invite_invalid")
	case errors.Is(err, auth.ErrUsernameTaken):
		writeError(w, http.StatusConflict, "username_taken")
	case errors.Is(err, auth.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, auth.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, auth.ErrInvalidJSON), errors.Is(err, errJSON), errors.Is(err, mods.ErrInvalidJSON):
		writeError(w, http.StatusBadRequest, "invalid_json")
	case errors.Is(err, mods.ErrInvalidModsDir):
		writeError(w, http.StatusBadRequest, "invalid_mods_dir")
	case errors.Is(err, mods.ErrModsDirUnset):
		writeError(w, http.StatusBadRequest, "mods_dir_unset")
	case errors.Is(err, mods.ErrInvalidFilename):
		writeError(w, http.StatusBadRequest, "invalid_filename")
	case errors.Is(err, mods.ErrInvalidDest):
		writeError(w, http.StatusBadRequest, "invalid_dest")
	case errors.Is(err, mods.ErrInvalidVersion):
		writeError(w, http.StatusBadRequest, "invalid_version")
	case errors.Is(err, mods.ErrJarRead):
		writeError(w, http.StatusBadRequest, mods.ErrJarRead.Error())
	case errors.Is(err, mods.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, mods.ErrConflict):
		writeError(w, http.StatusConflict, "conflict")
	case errors.Is(err, mods.ErrSlugExists):
		writeError(w, http.StatusConflict, "slug_exists")
	case errors.Is(err, mods.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, mods.ErrPayloadTooLarge), errors.Is(err, errPayload):
		writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large")
	case errors.Is(err, mods.ErrBackupBusy):
		writeError(w, http.StatusConflict, "backup_busy")
	case errors.Is(err, mods.ErrBackupDenied):
		writeError(w, http.StatusBadGateway, mods.ErrBackupDenied.Error())
	case errors.Is(err, mods.ErrBackupCopy):
		writeError(w, http.StatusBadGateway, mods.ErrBackupCopy.Error())
	case errors.Is(err, mods.ErrBadBackupTime):
		writeError(w, http.StatusBadRequest, mods.ErrBadBackupTime.Error())
	case errors.Is(err, mods.ErrBadBackupScope):
		writeError(w, http.StatusBadRequest, mods.ErrBadBackupScope.Error())
	case errors.Is(err, mods.ErrNoWorldLevel):
		writeError(w, http.StatusBadRequest, mods.ErrNoWorldLevel.Error())
	case errors.Is(err, mods.ErrBadWorldZip):
		writeError(w, http.StatusBadRequest, mods.ErrBadWorldZip.Error())
	case errors.Is(err, mods.ErrNotDatapack):
		writeError(w, http.StatusBadRequest, mods.ErrNotDatapack.Error())
	case errors.Is(err, mods.ErrBadDatapack):
		writeError(w, http.StatusBadRequest, mods.ErrBadDatapack.Error())
	case errors.Is(err, mods.ErrBadDatapackName):
		writeError(w, http.StatusBadRequest, mods.ErrBadDatapackName.Error())
	case errors.Is(err, mods.ErrDatapackWrite):
		writeError(w, http.StatusBadGateway, mods.ErrDatapackWrite.Error())
	case errors.Is(err, mods.ErrServerProperties):
		writeError(w, http.StatusBadGateway, mods.ErrServerProperties.Error())
	case errors.Is(err, mods.ErrDatapackPath):
		writeError(w, http.StatusBadRequest, mods.ErrDatapackPath.Error())
	case errors.Is(err, mods.ErrDatapackText):
		writeError(w, http.StatusBadRequest, mods.ErrDatapackText.Error())
	case errors.Is(err, mods.ErrDatapackFile):
		writeError(w, http.StatusNotFound, mods.ErrDatapackFile.Error())
	case errors.Is(err, mods.ErrDatapackFolder):
		writeError(w, http.StatusNotFound, mods.ErrDatapackFolder.Error())
	case errors.Is(err, mods.ErrDatapackBig):
		writeError(w, http.StatusBadRequest, mods.ErrDatapackBig.Error())
	case errors.Is(err, mods.ErrBackupMissing):
		writeError(w, http.StatusNotFound, mods.ErrBackupMissing.Error())
	case errors.Is(err, mods.ErrNeedMilestone):
		writeError(w, http.StatusConflict, mods.ErrNeedMilestone.Error())
	case errors.Is(err, mods.ErrBackupRunning):
		writeError(w, http.StatusConflict, mods.ErrBackupRunning.Error())
	case errors.Is(err, mods.ErrMilestoneUsed):
		writeError(w, http.StatusConflict, mods.ErrMilestoneUsed.Error())
	case errors.Is(err, mods.ErrS3Access):
		writeError(w, http.StatusBadGateway, mods.ErrS3Access.Error())
	case errors.Is(err, mods.ErrS3Config):
		writeError(w, http.StatusBadRequest, mods.ErrS3Config.Error())
	case errors.Is(err, mods.ErrRollbackFailed):
		writeError(w, http.StatusBadGateway, "rollback_failed")
	case errors.Is(err, minecraft.ErrRCON):
		writeError(w, http.StatusBadGateway, "rcon_failed")
	case errors.Is(err, minecraft.ErrRestart):
		writeError(w, http.StatusBadGateway, "restart_failed")
	case errors.Is(err, minecraft.ErrLogs):
		writeError(w, http.StatusBadGateway, "logs_failed")
	case errors.Is(err, minecraft.ErrBusy):
		writeError(w, http.StatusConflict, "restart_in_progress")
	case errors.Is(err, minecraft.ErrInvalidLimit), errors.Is(err, mods.ErrInvalidLimit), errors.Is(err, errLimit):
		writeError(w, http.StatusBadRequest, "invalid_limit")
	default:
		writeError(w, http.StatusInternalServerError, "internal")
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	err := json.NewDecoder(r.Body).Decode(dst)
	if err == io.EOF {
		return io.EOF
	}
	if tooLarge(err) {
		return errPayload
	}
	if err != nil {
		return errJSON
	}
	return nil
}

func tooLarge(err error) bool {
	if err == nil {
		return false
	}
	var mb *http.MaxBytesError
	if errors.As(err, &mb) {
		return true
	}
	return strings.Contains(err.Error(), "request body too large")
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

type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func (l *limiter) allow(host string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(host, time.Now())
	return len(l.hits[host]) < 10
}

func (l *limiter) fail(host string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.prune(host, now)
	l.hits[host] = append(l.hits[host], now)
}

func (l *limiter) success(host string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, host)
}

func (l *limiter) prune(host string, now time.Time) {
	cut := now.Add(-10 * time.Minute)
	old := l.hits[host]
	kept := old[:0]
	for _, t := range old {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.hits, host)
		return
	}
	l.hits[host] = kept
}

func countsFailure(err error) bool {
	return errors.Is(err, auth.ErrUnauthorized) ||
		errors.Is(err, auth.ErrInviteInvalid) ||
		errors.Is(err, auth.ErrUsernameTaken)
}
