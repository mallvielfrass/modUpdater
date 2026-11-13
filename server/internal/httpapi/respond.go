package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"modupdater/internal/auth"
	"modupdater/internal/minecraft"
	"modupdater/internal/mods"
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
