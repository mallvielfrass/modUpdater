package mods

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/klauspost/compress/zstd"
	"modupdater/internal/chunkbackup"
	"modupdater/internal/store"
)

const (
	labelManual   = "ручной"
	labelRestart  = "перед перезапуском"
	labelRollback = "перед откатом"
	labelDaily    = "ежедневный"
	labelUpload   = "загруженный"
	labelLaunch   = "перед запуском"

	backupScopeWorld = "world"
	backupScopeFull  = "full"
	backupScopeMods  = "mods"
)

var (
	ErrBackupBusy     = errors.New("backup_busy")
	ErrBackupDenied   = errors.New("Нет доступа к каталогу сервера.")
	ErrBackupCopy     = errors.New("Не удалось скопировать файлы сервера.")
	ErrBadBackupTime  = errors.New("Некорректное время бекапа.")
	ErrBadBackupScope = errors.New("Некорректная область бекапа.")
	ErrRollbackFailed = errors.New("rollback_failed")
	ErrNoWorldLevel   = errors.New("В архиве нет level.dat.")
	ErrBadWorldZip    = errors.New("Архив мира повреждён.")
	ErrBackupMissing  = errors.New("Бекап не найден.")
	ErrNeedMilestone  = errors.New("Сначала создайте веху.")
	ErrBackupRunning  = errors.New("Бекап уже выполняется.")
	ErrMilestoneUsed  = errors.New("Сначала удалите снапшоты этой вехи.")

	renameFile = os.Rename
)

type BackupAccessError struct {
	Path string
	File bool
}

func (e *BackupAccessError) Error() string {
	if e == nil || strings.TrimSpace(e.Path) == "" {
		return ErrBackupDenied.Error()
	}
	kind := "каталогу"
	if e.File {
		kind = "файлу"
	}
	return "Нет доступа к " + kind + " " + e.Path + "."
}

func (e *BackupAccessError) Unwrap() error {
	return ErrBackupDenied
}

func accessError(path string, file bool) error {
	return &BackupAccessError{Path: path, File: file}
}

func (s *Service) Snapshot(label string) (store.Backup, error) {
	if !s.mu.TryLock() {
		return store.Backup{}, ErrBackupRunning
	}
	defer s.mu.Unlock()
	return s.snapshot(label)
}

func (s *Service) ManualSnapshot() (store.Backup, error) {
	return s.Snapshot(labelManual)
}

func (s *Service) SnapshotForRestart() error {
	_, err := s.Snapshot(labelRestart)
	return err
}

func (s *Service) DailyTime() (string, error) {
	hhmm, _, err := s.db.DailyBackup()
	return hhmm, err
}

func (s *Service) SetDailyTime(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		if _, _, ok := parseHHMM(raw); !ok {
			return "", ErrBadBackupTime
		}
	}
	if err := s.db.SetDailyBackup(raw); err != nil {
		return "", err
	}
	return raw, nil
}

func (s *Service) BackupScope() (string, error) {
	raw, err := s.db.BackupScope()
	if err != nil {
		return "", err
	}
	return normalizeBackupScope(raw), nil
}

func (s *Service) SetBackupScope(raw string) (string, error) {
	switch strings.TrimSpace(raw) {
	case "", backupScopeWorld:
		raw = backupScopeWorld
	case backupScopeFull:
		raw = backupScopeFull
	case backupScopeMods:
		raw = backupScopeMods
	default:
		return "", ErrBadBackupScope
	}
	if err := s.db.SetBackupScope(raw); err != nil {
		return "", err
	}
	return raw, nil
}

func normalizeBackupScope(raw string) string {
	switch strings.TrimSpace(raw) {
	case backupScopeFull:
		return backupScopeFull
	case backupScopeMods:
		return backupScopeMods
	default:
		return backupScopeWorld
	}
}

func knownBackupScope(scope string) bool {
	switch scope {
	case backupScopeWorld, backupScopeFull, backupScopeMods:
		return true
	default:
		return false
	}
}

func (s *Service) RunDaily() {
	_ = s.DailyOnce(time.Now())
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for now := range tick.C {
		_ = s.DailyOnce(now)
	}
}

func (s *Service) DailyOnce(now time.Time) error {
	if !s.mu.TryLock() {
		return nil
	}
	defer s.mu.Unlock()
	hhmm, day, err := s.db.DailyBackup()
	if err != nil {
		return err
	}
	if !DueDaily(now, hhmm, day) {
		return nil
	}
	if _, err := s.snapshot(labelDaily); err != nil {
		if errors.Is(err, ErrNeedMilestone) {
			return err
		}
		_ = s.db.SetDailyBackupDay(now.In(time.Local).Format("2006-01-02"))
		return err
	}
	return s.db.SetDailyBackupDay(now.In(time.Local).Format("2006-01-02"))
}

func DueDaily(now time.Time, hhmm, lastDay string) bool {
	hhmm = strings.TrimSpace(hhmm)
	if hhmm == "" {
		return false
	}
	h, m, ok := parseHHMM(hhmm)
	if !ok {
		return false
	}
	now = now.In(time.Local)
	scheduled := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, time.Local)
	if now.Before(scheduled) {
		return false
	}
	return lastDay != now.Format("2006-01-02")
}

func parseHHMM(s string) (int, int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, 0, false
	}
	for i, c := range s {
		if i == 2 {
			continue
		}
		if c < '0' || c > '9' {
			return 0, 0, false
		}
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	if h > 23 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func (s *Service) ListBackups() ([]store.Backup, error) {
	list, err := s.db.ListBackups()
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []store.Backup{}
	}
	for i := range list {
		if list[i].Status == "running" {
			continue
		}
		if list[i].Size >= 0 && list[i].SHA256 != "" {
			continue
		}
		size, sum, err := s.fingerprint(list[i])
		if err != nil {
			return nil, err
		}
		if err := s.db.SetBackupFingerprint(list[i].ID, size, sum); err != nil {
			return nil, err
		}
		list[i].Size = size
		list[i].SHA256 = sum
	}
	return list, nil
}

func (s *Service) Rollback(id string) ([]store.Backup, string, []store.PendingRestart, error) {
	if !s.mu.TryLock() {
		return nil, "", nil, ErrBackupRunning
	}
	defer s.mu.Unlock()
	if !store.ValidID(id) {
		return nil, "", nil, ErrNotFound
	}
	if _, err := s.db.GetBackup(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, "", nil, ErrNotFound
		}
		return nil, "", nil, err
	}
	scope, err := s.scopeOf(id)
	if err != nil {
		return nil, "", nil, err
	}
	safety, err := s.capture(labelRollback, backupKindSnapshot, scope)
	if err != nil {
		return nil, "", nil, err
	}
	modsDir, configDir, worldDir, err := s.layout()
	if err != nil {
		return nil, "", nil, err
	}
	root, err := s.backupRoot()
	if err != nil {
		return nil, "", nil, mapSnapshot(err)
	}
	pairs, err := s.restorePairs(root, id, modsDir, configDir, worldDir)
	if err != nil {
		return nil, "", nil, mapRollback(err)
	}
	var files, nbytes int64
	for _, pair := range pairs {
		n, b, merr := measurePair(pair)
		if merr != nil {
			return nil, "", nil, mapRollback(merr)
		}
		files += n
		nbytes += b
	}
	s.beginJob(files, nbytes, "разворот")
	defer s.endJob()
	if err := swapAll(pairs, &treeNote{bytes: s.addBytes, file: s.addFile}); err != nil {
		return nil, "", nil, mapRollback(err)
	}
	if err := s.db.SetCurrentBackup(id); err != nil {
		backPairs, pairErr := s.restorePairs(root, safety.ID, modsDir, configDir, worldDir)
		if pairErr != nil {
			return nil, "", nil, ErrRollbackFailed
		}
		if backErr := swapAll(backPairs, nil); backErr != nil {
			return nil, "", nil, ErrRollbackFailed
		}
		if errors.Is(err, store.ErrNotFound) {
			return nil, "", nil, ErrNotFound
		}
		return nil, "", nil, ErrRollbackFailed
	}
	if err := s.noteServiceStart(); err != nil {
		return nil, "", nil, err
	}
	last, pending, err := s.db.MarkRollbackPending()
	if err != nil {
		return nil, "", nil, err
	}
	list, err := s.ListBackups()
	if err != nil {
		return nil, "", nil, err
	}
	return list, last, pending, nil
}

func (s *Service) StageLaunch(id string) error {
	if !s.mu.TryLock() {
		return ErrBackupRunning
	}
	defer s.mu.Unlock()
	if !store.ValidID(id) {
		return ErrNotFound
	}
	if _, err := s.db.GetBackup(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	scope, err := s.scopeOf(id)
	if err != nil {
		return err
	}
	safety, err := s.capture(labelLaunch, backupKindSnapshot, scope)
	if err != nil {
		return err
	}
	return s.placeBackup(id, safety.ID, "запуск")
}

func (s *Service) placeBackup(id, safetyID, phase string) error {
	modsDir, configDir, worldDir, err := s.layout()
	if err != nil {
		return err
	}
	root, err := s.backupRoot()
	if err != nil {
		return mapSnapshot(err)
	}
	pairs, err := s.restorePairs(root, id, modsDir, configDir, worldDir)
	if err != nil {
		return mapRollback(err)
	}
	var files, nbytes int64
	for _, pair := range pairs {
		n, b, merr := measurePair(pair)
		if merr != nil {
			return mapRollback(merr)
		}
		files += n
		nbytes += b
	}
	s.beginJob(files, nbytes, phase)
	defer s.endJob()
	if err := swapAll(pairs, &treeNote{bytes: s.addBytes, file: s.addFile}); err != nil {
		return mapRollback(err)
	}
	if err := s.db.SetCurrentBackup(id); err != nil {
		backPairs, pairErr := s.restorePairs(root, safetyID, modsDir, configDir, worldDir)
		if pairErr != nil {
			return ErrRollbackFailed
		}
		if backErr := swapAll(backPairs, nil); backErr != nil {
			return ErrRollbackFailed
		}
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		return ErrRollbackFailed
	}
	return nil
}

func (s *Service) BackupZipName(id string) (string, error) {
	b, dir, err := s.readyBackup(id)
	if err != nil {
		return "", err
	}
	_ = dir
	return backupFileName(b), nil
}

func (s *Service) WriteBackupZip(id string, w io.Writer) error {
	_, dir, err := s.readyBackup(id)
	if err != nil {
		return err
	}
	if dir == "" {
		return s.writeChunkArchive(id, w)
	}
	enc, err := zstd.NewWriter(w)
	if err != nil {
		return ErrBackupCopy
	}
	tw := tar.NewWriter(enc)
	writeErr := writeBackupTar(tw, dir)
	closeTar := tw.Close()
	closeZst := enc.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeTar != nil || closeZst != nil {
		return ErrBackupCopy
	}
	return nil
}

func (s *Service) writeChunkArchive(id string, w io.Writer) error {
	root, err := s.backupRoot()
	if err != nil {
		return mapSnapshot(err)
	}
	man, err := chunkbackup.LoadManifest(chunkbackup.ManifestPath(root, id))
	if err != nil {
		return ErrBackupMissing
	}
	var parent *chunkbackup.Manifest
	if man.Parent != "" {
		p, err := chunkbackup.LoadManifest(chunkbackup.ManifestPath(root, man.Parent))
		if err != nil {
			return ErrBackupCopy
		}
		parent = &p
	}
	st, err := s.openChunks()
	if err != nil {
		return mapSnapshot(err)
	}
	if err := chunkbackup.WriteArchive(w, st, man, parent); err != nil {
		return ErrBackupCopy
	}
	return nil
}

func writeBackupTar(tw *tar.Writer, dir string) error {
	for _, name := range []string{"mods", "config", "world"} {
		src := filepath.Join(dir, name)
		st, err := os.Lstat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return ErrBackupCopy
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			continue
		}
		if err := addBackupTar(tw, src, name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) readyBackup(id string) (store.Backup, string, error) {
	if !store.ValidID(id) {
		return store.Backup{}, "", ErrBackupMissing
	}
	b, err := s.db.GetBackup(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Backup{}, "", ErrBackupMissing
		}
		return store.Backup{}, "", err
	}
	root, err := s.backupRoot()
	if err != nil {
		return store.Backup{}, "", mapSnapshot(err)
	}
	if legacyBackup(root, id) {
		return b, filepath.Join(root, id), nil
	}
	man := chunkbackup.ManifestPath(root, id)
	st, err := os.Lstat(man)
	if err != nil || !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
		return store.Backup{}, "", ErrBackupMissing
	}
	return b, "", nil
}

func backupFileName(b store.Backup) string {
	var digits strings.Builder
	for _, r := range b.CreatedAt {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	stamp := digits.String()
	if stamp == "" {
		stamp = "backup"
	}
	return "backup-" + stamp + "-" + b.ID + ".tar.zst"
}

func addBackupTar(tw *tar.Writer, src, prefix string) error {
	src = filepath.Clean(src)
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return ErrBackupCopy
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ErrBackupCopy
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return ErrBackupCopy
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() {
			return nil
		}
		name := prefix
		if rel != "." {
			name = prefix + "/" + strings.ReplaceAll(rel, string(filepath.Separator), "/")
		}
		var file *os.File
		if info.IsDir() {
			if !strings.HasSuffix(name, "/") {
				name += "/"
			}
		} else {
			opened, openErr := os.Open(path)
			if openErr != nil {
				if errors.Is(openErr, os.ErrPermission) {
					return nil
				}
				return ErrBackupCopy
			}
			defer opened.Close()
			file = opened
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return ErrBackupCopy
		}
		hdr.Name = name
		hdr.Format = tar.FormatUSTAR
		if err := tw.WriteHeader(hdr); err != nil {
			hdr.Format = 0
			if err = tw.WriteHeader(hdr); err != nil {
				return ErrBackupCopy
			}
		}
		if file == nil {
			return nil
		}
		if _, err := io.Copy(tw, file); err != nil {
			return ErrBackupCopy
		}
		return nil
	})
}

func (s *Service) ImportWorld(r io.Reader) (store.Backup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dataDir, 0o755); err != nil {
		return store.Backup{}, ErrBackupCopy
	}
	tmp, err := os.CreateTemp(s.dataDir, "world-*.zip")
	if err != nil {
		return store.Backup{}, ErrBackupCopy
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return store.Backup{}, ErrBadWorldZip
	}
	if err := tmp.Close(); err != nil {
		return store.Backup{}, ErrBadWorldZip
	}
	root, err := s.backupRoot()
	if err != nil {
		return store.Backup{}, mapSnapshot(err)
	}
	id, err := store.NewID()
	if err != nil {
		return store.Backup{}, ErrBackupCopy
	}
	dest := filepath.Join(root, id)
	worldDir := filepath.Join(dest, "world")
	if err := os.MkdirAll(worldDir, 0o755); err != nil {
		os.RemoveAll(dest)
		return store.Backup{}, mapSnapshot(err)
	}
	if err := extractWorldZip(tmpName, worldDir); err != nil {
		os.RemoveAll(dest)
		return store.Backup{}, err
	}
	size, sum, err := chunkbackup.MeasureDir(dest)
	if err != nil {
		os.RemoveAll(dest)
		return store.Backup{}, mapSnapshot(err)
	}
	b := store.Backup{ID: id, CreatedAt: store.Now(), Label: labelUpload, Current: false, Size: size, SHA256: sum, Storage: "disk"}
	if err := s.db.AddBackupInactive(b); err != nil {
		os.RemoveAll(dest)
		return store.Backup{}, ErrBackupCopy
	}
	return b, nil
}

func extractWorldZip(zipPath, worldDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return ErrBadWorldZip
	}
	defer zr.Close()
	worldDir = filepath.Clean(worldDir)
	found := false
	for _, f := range zr.File {
		rel, ok := safeZipRel(f.Name)
		if !ok {
			return ErrBadWorldZip
		}
		if rel == "" {
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return ErrBadWorldZip
		}
		target := filepath.Join(worldDir, filepath.FromSlash(rel))
		target = filepath.Clean(target)
		if !pathInside(worldDir, target) {
			return ErrBadWorldZip
		}
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return ErrBackupCopy
			}
			continue
		}
		if rel == "level.dat" {
			found = true
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return ErrBackupCopy
		}
		rc, err := f.Open()
		if err != nil {
			return ErrBadWorldZip
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			rc.Close()
			return ErrBackupCopy
		}
		_, copyErr := io.Copy(out, rc)
		closeErr := out.Close()
		rc.Close()
		if copyErr != nil {
			return ErrBackupCopy
		}
		if closeErr != nil {
			return ErrBackupCopy
		}
	}
	if !found {
		return ErrNoWorldLevel
	}
	return nil
}

func safeZipRel(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.ContainsRune(name, 0) {
		return "", false
	}
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return "", true
	}
	var parts []string
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", false
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "/"), true
}

func (s *Service) snapshot(label string) (store.Backup, error) {
	return s.capture(label, backupKindSnapshot, "")
}

func (s *Service) BackupDisk() (int64, int64, error) {
	root := filepath.Join(s.dataDir, "backups")
	used, err := storeBytes(root)
	if err != nil {
		return 0, 0, err
	}
	probe := root
	if _, err := os.Lstat(root); err != nil {
		probe = s.dataDir
	}
	free, err := diskFree(probe)
	if err != nil {
		return 0, 0, err
	}
	return used, free, nil
}

func storeBytes(root string) (int64, error) {
	st, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return 0, nil
	}
	var n int64
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		n += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

func diskFree(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	bsize := st.Frsize
	if bsize <= 0 {
		bsize = st.Bsize
	}
	if bsize <= 0 || st.Bavail == 0 {
		return 0, nil
	}
	if st.Bavail > uint64(math.MaxInt64)/uint64(bsize) {
		return math.MaxInt64, nil
	}
	return int64(st.Bavail) * bsize, nil
}

func (s *Service) backupRoot() (string, error) {
	root, err := s.dataSubdir("backups")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(root, 0o755); err != nil {
		return "", err
	}
	return root, nil
}

func (s *Service) layout() (string, string, string, error) {
	modsDir, err := s.resolve()
	if err != nil {
		return "", "", "", err
	}
	root := filepath.Dir(modsDir)
	level := "world"
	propsPath := filepath.Join(root, "server.properties")
	props, err := readProperties(propsPath)
	if err != nil {
		if !os.IsNotExist(err) {
			if errors.Is(err, os.ErrPermission) {
				return "", "", "", accessError(propsPath, true)
			}
			return "", "", "", mapSnapshot(err)
		}
	} else if name := strings.TrimSpace(props["level-name"]); name != "" {
		level = name
	}
	worldPath, err := resolveServerPath(root, level)
	if err != nil {
		return "", "", "", ErrBackupCopy
	}
	return modsDir, filepath.Join(root, "config"), worldPath, nil
}

func (s *Service) present(path string, required bool) (string, error) {
	ok, err := readableDir(path)
	if err != nil {
		return "", err
	}
	if !ok {
		if required {
			return "", ErrBackupCopy
		}
		return "", nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return "", accessError(path, false)
		}
		return "", mapSnapshot(err)
	}
	real = filepath.Clean(real)
	st, err := os.Stat(real)
	if err != nil {
		if os.IsNotExist(err) && !required {
			return "", nil
		}
		if errors.Is(err, os.ErrPermission) {
			return "", accessError(path, false)
		}
		return "", mapSnapshot(err)
	}
	if !st.IsDir() {
		return "", ErrBackupCopy
	}
	if err := s.rejectStore(real); err != nil {
		return "", err
	}
	return real, nil
}

func readableDir(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		if errors.Is(err, os.ErrPermission) {
			return false, accessError(path, false)
		}
		return false, mapSnapshot(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return false, accessError(path, false)
		}
		return false, mapSnapshot(err)
	}
	if !st.IsDir() {
		return false, ErrBackupCopy
	}
	if _, err := f.Readdirnames(1); err != nil && err != io.EOF {
		if errors.Is(err, os.ErrPermission) {
			return false, accessError(path, false)
		}
		return false, mapSnapshot(err)
	}
	return true, nil
}

func (s *Service) rejectStore(dir string) error {
	dataAbs, err := filepath.Abs(s.dataDir)
	if err != nil {
		return ErrBackupCopy
	}
	dataAbs = filepath.Clean(dataAbs)
	backups := filepath.Join(dataAbs, "backups")
	if dir == dataAbs || dir == backups || pathInside(dataAbs, dir) || pathInside(dir, dataAbs) || pathInside(backups, dir) || pathInside(dir, backups) {
		return ErrBackupCopy
	}
	return nil
}

func resolveServerPath(root, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrBackupCopy
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), nil
	}
	clean := filepath.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrBackupCopy
	}
	full := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrBackupCopy
	}
	return full, nil
}

func readProperties(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			key, val, ok = strings.Cut(line, ":")
			if !ok {
				continue
			}
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "" {
			continue
		}
		out[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type dirPair struct {
	live   string
	src    string
	token  string
	fill   func(dst string, note *treeNote) error
	files  int64
	nbytes int64
}

type swapItem struct {
	live    string
	stage   string
	prev    string
	moved   bool
	placed  bool
	created bool
}

func swapAll(pairs []dirPair, note *treeNote) error {
	items := make([]swapItem, len(pairs))
	for i, pair := range pairs {
		if pair.token == "" || strings.Contains(pair.token, "/") || strings.Contains(pair.token, `\`) {
			return ErrRollbackFailed
		}
		if pair.fill == nil {
			st, err := os.Stat(pair.src)
			if err != nil || !st.IsDir() {
				return ErrRollbackFailed
			}
		}
		items[i] = swapItem{
			live:  pair.live,
			stage: pair.live + ".stage-" + pair.token,
			prev:  pair.live + ".prev-" + pair.token,
		}
		if _, err := os.Lstat(items[i].stage); err == nil {
			return ErrRollbackFailed
		}
		if _, err := os.Lstat(items[i].prev); err == nil {
			return ErrRollbackFailed
		}
	}
	ok := false
	defer func() {
		if !ok {
			undoSwaps(items)
		}
	}()
	for i, pair := range pairs {
		var err error
		if pair.fill != nil {
			if err = os.MkdirAll(items[i].stage, 0o755); err != nil {
				return ErrRollbackFailed
			}
			err = pair.fill(items[i].stage, note)
		} else {
			err = copyTree(pair.src, items[i].stage, note)
		}
		if err != nil {
			return err
		}
	}
	for i := range items {
		if _, err := os.Lstat(items[i].live); errors.Is(err, os.ErrNotExist) {
			if err := os.MkdirAll(filepath.Dir(items[i].live), 0o755); err != nil {
				return ErrRollbackFailed
			}
			if err := renameFile(items[i].stage, items[i].live); err != nil {
				if isBusy(err) {
					return ErrBackupBusy
				}
				return ErrRollbackFailed
			}
			items[i].created = true
			items[i].placed = true
			continue
		}
		if err := renameFile(items[i].live, items[i].prev); err != nil {
			if isBusy(err) {
				return ErrBackupBusy
			}
			return ErrRollbackFailed
		}
		items[i].moved = true
		if err := renameFile(items[i].stage, items[i].live); err != nil {
			if isBusy(err) {
				return ErrBackupBusy
			}
			return ErrRollbackFailed
		}
		items[i].placed = true
	}
	for i := range items {
		_ = os.RemoveAll(items[i].prev)
	}
	ok = true
	return nil
}

func (s *Service) restorePairs(root, id, modsDir, configDir, worldDir string) ([]dirPair, error) {
	if !legacyBackup(root, id) {
		st, err := s.openChunks()
		if err != nil {
			return nil, mapRollback(err)
		}
		return chunkRestorePairs(st, root, id, modsDir, configDir, worldDir)
	}
	var out []dirPair
	add := func(live, name string) error {
		if live == "" {
			return nil
		}
		src := filepath.Join(root, id, name)
		st, err := os.Stat(src)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !st.IsDir() {
			return ErrRollbackFailed
		}
		out = append(out, dirPair{live: live, src: src, token: id})
		return nil
	}
	if err := add(modsDir, "mods"); err != nil {
		return nil, err
	}
	if err := add(configDir, "config"); err != nil {
		return nil, err
	}
	if err := add(worldDir, "world"); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ErrRollbackFailed
	}
	return out, nil
}

func undoSwaps(items []swapItem) {
	for i := len(items) - 1; i >= 0; i-- {
		it := &items[i]
		if it.created {
			_ = os.RemoveAll(it.live)
			it.created = false
			it.placed = false
			continue
		}
		if it.placed {
			_ = renameFile(it.live, it.stage)
			_ = renameFile(it.prev, it.live)
			_ = os.RemoveAll(it.stage)
			it.placed = false
			it.moved = false
			continue
		}
		if it.moved {
			_ = renameFile(it.prev, it.live)
			it.moved = false
		}
		_ = os.RemoveAll(it.stage)
	}
}

type CopyProgress struct {
	Running    bool   `json:"running"`
	Phase      string `json:"phase"`
	FilesDone  int64  `json:"files_done"`
	FilesTotal int64  `json:"files_total"`
	BytesDone  int64  `json:"bytes_done"`
	BytesTotal int64  `json:"bytes_total"`
}

type progressSlot struct {
	mu  sync.Mutex
	cur CopyProgress
}

type treeNote struct {
	bytes func(int64)
	file  func()
}

func (s *Service) BackupProgress() CopyProgress {
	s.prog.mu.Lock()
	defer s.prog.mu.Unlock()
	return s.prog.cur
}

func (s *Service) beginJob(files, nbytes int64, phase string) {
	s.prog.mu.Lock()
	s.prog.cur = CopyProgress{
		Running:    true,
		Phase:      phase,
		FilesTotal: files,
		BytesTotal: nbytes,
	}
	s.prog.mu.Unlock()
}

func (s *Service) endJob() {
	s.prog.mu.Lock()
	s.prog.cur.Running = false
	s.prog.mu.Unlock()
}

func (s *Service) addBytes(n int64) {
	if n <= 0 {
		return
	}
	s.prog.mu.Lock()
	s.prog.cur.BytesDone += n
	s.prog.mu.Unlock()
}

func (s *Service) addFile() {
	s.prog.mu.Lock()
	s.prog.cur.FilesDone++
	s.prog.mu.Unlock()
}

func measureDir(src string) (int64, int64, error) {
	var files, nbytes int64
	src = filepath.Clean(src)
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if isBusy(err) {
				return ErrBackupBusy
			}
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ErrBackupCopy
		}
		info, err := d.Info()
		if err != nil {
			if isBusy(err) {
				return ErrBackupBusy
			}
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || d.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		files++
		nbytes += info.Size()
		return nil
	})
	if err != nil {
		return 0, 0, mapSnapshot(err)
	}
	return files, nbytes, nil
}

func copyTree(src, dst string, note *treeNote) error {
	src = filepath.Clean(src)
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if isBusy(err) {
				return ErrBackupBusy
			}
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ErrBackupCopy
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			if isBusy(err) {
				return ErrBackupBusy
			}
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if err := copyLink(path, target); err != nil {
				if errors.Is(err, os.ErrPermission) {
					return nil
				}
				return err
			}
			return nil
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		var onBytes func(int64)
		if note != nil {
			onBytes = note.bytes
		}
		if err := copyFile(path, target, 0o644, onBytes); err != nil {
			if errors.Is(err, os.ErrPermission) {
				return nil
			}
			return err
		}
		if note != nil && note.file != nil {
			note.file()
		}
		return nil
	})
}

func copyLink(src, dst string) error {
	link, err := os.Readlink(src)
	if err != nil {
		if isBusy(err) {
			return ErrBackupBusy
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Symlink(link, dst)
}

func copyFile(src, dst string, perm os.FileMode, onBytes func(int64)) error {
	in, err := os.Open(src)
	if err != nil {
		if isBusy(err) {
			return ErrBackupBusy
		}
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if perm == 0 {
		perm = 0o644
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	var reader io.Reader = in
	if onBytes != nil {
		reader = &byteTap{r: in, emit: onBytes}
	}
	_, copyErr := io.Copy(out, reader)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(dst, perm)
}

type byteTap struct {
	r    io.Reader
	emit func(int64)
	pend int64
}

func (b *byteTap) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if n > 0 {
		b.pend += int64(n)
	}
	if b.pend > 0 && (b.pend >= 1<<20 || err != nil) {
		b.emit(b.pend)
		b.pend = 0
	}
	return n, err
}

func isBusy(err error) bool {
	return errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.ETXTBSY)
}

func mapSnapshot(err error) error {
	if err == nil || errors.Is(err, ErrBackupBusy) || errors.Is(err, ErrBackupDenied) || errors.Is(err, ErrBackupCopy) || errors.Is(err, ErrModsDirUnset) || errors.Is(err, ErrInvalidModsDir) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrNeedMilestone) || errors.Is(err, ErrBackupMissing) || errors.Is(err, ErrBackupRunning) {
		return err
	}
	if isBusy(err) {
		return ErrBackupBusy
	}
	if errors.Is(err, os.ErrPermission) {
		return ErrBackupDenied
	}
	return ErrBackupCopy
}

func mapRollback(err error) error {
	if err == nil || errors.Is(err, ErrBackupBusy) || errors.Is(err, ErrRollbackFailed) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrModsDirUnset) || errors.Is(err, ErrNeedMilestone) {
		return err
	}
	if isBusy(err) {
		return ErrBackupBusy
	}
	return ErrRollbackFailed
}
