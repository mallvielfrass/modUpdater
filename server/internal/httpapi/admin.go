package httpapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"modupdater/internal/minecraft"
	"modupdater/internal/mods"
	"modupdater/internal/store"
)

func (a *API) getSettings(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	dir, err := a.modsDir()
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		ModsDir string `json:"mods_dir"`
	}{ModsDir: dir})
}

func (a *API) modsDir() (string, error) {
	return a.mods.StoredDir()
}

func (a *API) putSettings(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var body struct {
		ModsDir *string `json:"mods_dir"`
	}
	if err := readJSON(w, r, &body); err != nil || body.ModsDir == nil {
		if errors.Is(err, errPayload) {
			writeMapped(w, err)
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	res, err := a.mods.SetModsDir(*body.ModsDir)
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) scanMods(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	res, err := a.mods.Scan()
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) adminMods(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	list, err := a.mods.ListAll()
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
	last, pending, err := a.mods.RestartStatus()
	if err != nil {
		writeMapped(w, err)
		return
	}
	if pending == nil {
		pending = []store.PendingRestart{}
	}
	writeJSON(w, http.StatusOK, struct {
		Mods           []modJSON              `json:"mods"`
		LastRestartAt  string                 `json:"last_restart_at"`
		PendingRestart []store.PendingRestart `json:"pending_restart"`
	}{Mods: views, LastRestartAt: last, PendingRestart: pending})
}

func (a *API) revokeMod(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	m, err := a.mods.Revoke(p["id"])
	if err != nil {
		writeMapped(w, err)
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

func (a *API) restoreMod(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	m, err := a.mods.Restore(p["id"])
	if err != nil {
		writeMapped(w, err)
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

func (a *API) addMod(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	up, err := readModForm(w, r, true)
	if err != nil {
		writeMapped(w, err)
		return
	}
	defer cleanupForm(r)
	if up.File != nil {
		defer up.File.Close()
	}
	m, err := a.mods.Add(up)
	if err != nil {
		writeMapped(w, err)
		return
	}
	view, err := a.modJSON(m)
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Mod modJSON `json:"mod"`
	}{Mod: view})
}

func (a *API) updateMod(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	up, err := readModForm(w, r, false)
	if err != nil {
		writeMapped(w, err)
		return
	}
	defer cleanupForm(r)
	if up.File != nil {
		defer up.File.Close()
	}
	m, err := a.mods.Update(p["id"], up)
	if err != nil {
		writeMapped(w, err)
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

func (a *API) deleteMod(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	if err := a.mods.Delete(p["id"]); err != nil {
		writeMapped(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listBackups(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	list, err := a.mods.ListBackups()
	if err != nil {
		writeMapped(w, err)
		return
	}
	daily, err := a.mods.DailyTime()
	if err != nil {
		writeMapped(w, err)
		return
	}
	scope, err := a.mods.BackupScope()
	if err != nil {
		writeMapped(w, err)
		return
	}
	s3, err := a.mods.S3Public()
	if err != nil {
		writeMapped(w, err)
		return
	}
	used, free, err := a.mods.BackupDisk()
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Backups     []store.Backup    `json:"backups"`
		DailyTime   string            `json:"daily_time"`
		BackupScope string            `json:"backup_scope"`
		BackupBytes int64             `json:"backup_bytes"`
		FreeBytes   int64             `json:"free_bytes"`
		Progress    mods.CopyProgress `json:"progress"`
		S3          store.S3Public    `json:"s3"`
	}{Backups: list, DailyTime: daily, BackupScope: scope, BackupBytes: used, FreeBytes: free, Progress: a.mods.BackupProgress(), S3: s3})
}

func (a *API) putBackupScope(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var body struct {
		Scope string `json:"scope"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeMapped(w, err)
		return
	}
	saved, err := a.mods.SetBackupScope(body.Scope)
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Scope string `json:"scope"`
	}{Scope: saved})
}

func (a *API) putBackupTime(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var body struct {
		Time string `json:"time"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeMapped(w, err)
		return
	}
	saved, err := a.mods.SetDailyTime(body.Time)
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Time string `json:"time"`
	}{Time: saved})
}

func (a *API) createMilestone(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	b, err := a.mods.Milestone()
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Backup store.Backup `json:"backup"`
	}{Backup: b})
}

type s3Body struct {
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

func s3FromBody(body s3Body) store.S3Settings {
	return store.S3Settings{
		Endpoint: body.Endpoint, Region: body.Region, Bucket: body.Bucket,
		Prefix: body.Prefix, AccessKey: body.AccessKey, Secret: body.SecretKey,
	}
}

func (a *API) putBackupDest(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var body struct {
		Dest string `json:"dest"`
	}
	if err := readJSON(w, r, &body); err != nil {
		writeMapped(w, err)
		return
	}
	switch strings.TrimSpace(body.Dest) {
	case "disk", "s3":
	default:
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := a.mods.SetBackupDest(strings.TrimSpace(body.Dest)); err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Dest string `json:"dest"`
	}{Dest: strings.TrimSpace(body.Dest)})
}

func (a *API) putS3(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var body s3Body
	if err := readJSON(w, r, &body); err != nil {
		writeMapped(w, err)
		return
	}
	saved, err := a.mods.SaveS3(s3FromBody(body))
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		S3 store.S3Public `json:"s3"`
	}{S3: saved})
}

func (a *API) checkS3(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var body s3Body
	if err := readJSON(w, r, &body); err != nil {
		writeMapped(w, err)
		return
	}
	msg, err := a.mods.CheckS3(s3FromBody(body))
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Message string `json:"message"`
	}{Message: msg})
}

func (a *API) deleteBackup(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	list, err := a.mods.DeleteBackup(p["id"])
	if err != nil {
		writeMapped(w, err)
		return
	}
	if list == nil {
		list = []store.Backup{}
	}
	writeJSON(w, http.StatusOK, struct {
		Backups []store.Backup `json:"backups"`
	}{Backups: list})
}

func (a *API) createBackup(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	b, err := a.mods.ManualSnapshot()
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Backup store.Backup `json:"backup"`
	}{Backup: b})
}

func (a *API) downloadBackup(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	name, err := a.mods.BackupZipName(p["id"])
	if err != nil {
		writeMapped(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zstd")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", name))
	if err := a.mods.WriteBackupZip(p["id"], w); err != nil {
		return
	}
}

func (a *API) uploadWorld(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	b, err := a.mods.ImportWorld(r.Body)
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Backup store.Backup `json:"backup"`
	}{Backup: b})
}

func (a *API) launchBackup(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	err := a.mc.RestartPrepared("", func() error {
		return a.mods.StageLaunch(p["id"])
	})
	if err != nil {
		writeMapped(w, err)
		return
	}
	last, pending, err := a.mods.MarkRestart()
	if err != nil {
		writeMapped(w, err)
		return
	}
	list, err := a.mods.ListBackups()
	if err != nil {
		writeMapped(w, err)
		return
	}
	if pending == nil {
		pending = []store.PendingRestart{}
	}
	if list == nil {
		list = []store.Backup{}
	}
	writeJSON(w, http.StatusOK, struct {
		Backups        []store.Backup         `json:"backups"`
		Warned         bool                   `json:"warned"`
		Restarted      bool                   `json:"restarted"`
		LastRestartAt  string                 `json:"last_restart_at"`
		PendingRestart []store.PendingRestart `json:"pending_restart"`
	}{Backups: list, Warned: true, Restarted: true, LastRestartAt: last, PendingRestart: pending})
}

func (a *API) rollbackBackup(w http.ResponseWriter, r *http.Request, p map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	list, last, pending, err := a.mods.Rollback(p["id"])
	if err != nil {
		writeMapped(w, err)
		return
	}
	if pending == nil {
		pending = []store.PendingRestart{}
	}
	writeJSON(w, http.StatusOK, struct {
		Backups        []store.Backup         `json:"backups"`
		LastRestartAt  string                 `json:"last_restart_at"`
		PendingRestart []store.PendingRestart `json:"pending_restart"`
	}{Backups: list, LastRestartAt: last, PendingRestart: pending})
}

func (a *API) restart(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if r.Body != nil {
		err := readJSON(w, r, &body)
		if err != nil && err != io.EOF {
			writeMapped(w, err)
			return
		}
	}
	err := a.mc.RestartBefore(body.Message, a.mods.SnapshotForRestart)
	backupError := ""
	if err != nil {
		if restartBlocked(err) {
			writeMapped(w, err)
			return
		}
		backupError = snapshotMessage(err)
	}
	last, pending, err := a.mods.MarkRestart()
	if err != nil {
		writeMapped(w, err)
		return
	}
	if pending == nil {
		pending = []store.PendingRestart{}
	}
	writeJSON(w, http.StatusOK, struct {
		Warned         bool                   `json:"warned"`
		Restarted      bool                   `json:"restarted"`
		LastRestartAt  string                 `json:"last_restart_at"`
		PendingRestart []store.PendingRestart `json:"pending_restart"`
		BackupError    string                 `json:"backup_error,omitempty"`
	}{Warned: true, Restarted: true, LastRestartAt: last, PendingRestart: pending, BackupError: backupError})
}

func restartBlocked(err error) bool {
	return errors.Is(err, minecraft.ErrRCON) || errors.Is(err, minecraft.ErrRestart) || errors.Is(err, minecraft.ErrBusy)
}

func snapshotMessage(err error) string {
	var access *mods.BackupAccessError
	if errors.As(err, &access) && access != nil && access.Path != "" {
		return access.Error()
	}
	switch {
	case errors.Is(err, mods.ErrBackupBusy):
		return "Файлы заняты, изменения не применены."
	case errors.Is(err, mods.ErrBackupDenied):
		return mods.ErrBackupDenied.Error()
	case errors.Is(err, mods.ErrBackupCopy):
		return mods.ErrBackupCopy.Error()
	case errors.Is(err, mods.ErrBadBackupTime):
		return mods.ErrBadBackupTime.Error()
	case errors.Is(err, mods.ErrModsDirUnset):
		return "Сначала укажите каталог модов на сервере."
	case errors.Is(err, mods.ErrInvalidModsDir):
		return "Некорректный каталог модов."
	case errors.Is(err, mods.ErrNotFound):
		return "Не найдено."
	case errors.Is(err, mods.ErrNeedMilestone):
		return mods.ErrNeedMilestone.Error()
	case errors.Is(err, mods.ErrBackupRunning):
		return mods.ErrBackupRunning.Error()
	case errors.Is(err, mods.ErrS3Config):
		return mods.ErrS3Config.Error()
	default:
		return "Не удалось сделать бекап."
	}
}

func (a *API) rconStatus(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	st, err := a.mc.Status()
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *API) logs(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	lines, err := boundedQuery(r, "lines", 200, 1, 5000)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_limit")
		return
	}
	unit, out, err := a.mc.Logs(lines, r.URL.Query().Get("since"))
	if err != nil {
		writeMapped(w, err)
		return
	}
	if out == nil {
		out = []string{}
	}
	writeJSON(w, http.StatusOK, struct {
		Unit  string   `json:"unit"`
		Lines []string `json:"lines"`
	}{Unit: unit, Lines: out})
}

func readModForm(w http.ResponseWriter, r *http.Request, fileRequired bool) (mods.Upload, error) {
	r.Body = http.MaxBytesReader(w, r.Body, mods.MaxJar+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		if tooLarge(err) {
			return mods.Upload{}, errPayload
		}
		return mods.Upload{}, errJSON
	}
	var up mods.Upload
	file, hdr, err := r.FormFile("file")
	if err != nil {
		if !fileRequired && errors.Is(err, http.ErrMissingFile) {
			file = nil
		} else {
			if tooLarge(err) {
				return mods.Upload{}, errPayload
			}
			return mods.Upload{}, errJSON
		}
	}
	if file != nil {
		up.File = file
		up.Filename = hdr.Filename
	}
	if r.MultipartForm != nil {
		if err := fillModFields(&up, r.MultipartForm); err != nil {
			if up.File != nil {
				up.File.Close()
			}
			r.MultipartForm.RemoveAll()
			return mods.Upload{}, err
		}
	}
	return up, nil
}

func fillModFields(up *mods.Upload, form *multipart.Form) error {
	if vs, ok := form.Value["name"]; ok && len(vs) > 0 {
		s := vs[0]
		up.Name = &s
	}
	if vs, ok := form.Value["description"]; ok && len(vs) > 0 {
		s := vs[0]
		up.Description = &s
	}
	if vs, ok := form.Value["client"]; ok && len(vs) > 0 {
		b, err := formBool(vs[0])
		if err != nil {
			return err
		}
		up.Client = &b
	}
	if vs, ok := form.Value["client_only"]; ok && len(vs) > 0 {
		b, err := formBool(vs[0])
		if err != nil {
			return err
		}
		up.ClientOnly = &b
	}
	if vs, ok := form.Value["client_required"]; ok && len(vs) > 0 {
		b, err := formBool(vs[0])
		if err != nil {
			return err
		}
		up.ClientRequired = &b
	}
	return nil
}

func formBool(v string) (bool, error) {
	switch v {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errJSON
	}
}

func cleanupForm(r *http.Request) {
	if r.MultipartForm != nil {
		r.MultipartForm.RemoveAll()
	}
}

func mediaType(r *http.Request) string {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return mt
}
