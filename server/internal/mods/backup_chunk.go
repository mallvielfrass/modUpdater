package mods

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"modupdater/internal/chunkbackup"
	"modupdater/internal/store"
)

const (
	labelMilestone      = "веха"
	backupKindMilestone = "milestone"
	backupKindSnapshot  = "snapshot"
)

func (s *Service) Milestone() (store.Backup, error) {
	if !s.mu.TryLock() {
		return store.Backup{}, ErrBackupRunning
	}
	defer s.mu.Unlock()
	return s.capture(labelMilestone, backupKindMilestone, "")
}

func (s *Service) capture(label, kind, scope string) (store.Backup, error) {
	if label == "" {
		return store.Backup{}, ErrBackupCopy
	}
	if scope == "" {
		var err error
		scope, err = s.scopeFor(label)
		if err != nil {
			return store.Backup{}, err
		}
	} else if !knownBackupScope(scope) {
		return store.Backup{}, ErrBadBackupScope
	}
	if _, _, _, err := s.layout(); err != nil {
		return store.Backup{}, err
	}
	parentID := ""
	if kind == backupKindSnapshot {
		pb, err := s.db.LatestMilestone(scope)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return store.Backup{}, ErrNeedMilestone
			}
			return store.Backup{}, err
		}
		parentID = pb.ID
	}
	dest, err := s.db.BackupDest()
	if err != nil {
		return store.Backup{}, err
	}
	if dest == "s3" {
		cfg, err := s.db.S3()
		if err != nil {
			return store.Backup{}, err
		}
		if !cfg.Ready() {
			return store.Backup{}, ErrS3Config
		}
	}
	id, err := store.NewID()
	if err != nil {
		return store.Backup{}, ErrBackupCopy
	}
	running := store.Backup{
		ID: id, CreatedAt: store.Now(), Label: label, Current: false,
		Kind: kind, Scope: scope, ParentID: parentID, Storage: "disk", Dest: dest, Status: "running",
	}
	if err := s.db.AddBackupInactive(running); err != nil {
		return store.Backup{}, ErrBackupCopy
	}
	var runErr error
	defer func() {
		if runErr != nil {
			_ = s.db.FailBackup(id, backupFailText(runErr))
		}
	}()
	var done store.Backup
	work := func() error {
		var copyErr error
		done, copyErr = s.captureNow(label, kind, scope, id, parentID)
		return copyErr
	}
	if scope == backupScopeMods || s.holdWorld == nil {
		runErr = work()
	} else {
		runErr = s.holdWorld(work)
	}
	if runErr != nil {
		return store.Backup{}, runErr
	}
	if err := s.db.FinishBackup(done.ID, done.Size, done.SHA256); err != nil {
		runErr = err
		return store.Backup{}, ErrBackupCopy
	}
	done.Current = true
	done.Status = ""
	return done, nil
}

func backupFailText(err error) string {
	var access *BackupAccessError
	if errors.As(err, &access) && access != nil && strings.TrimSpace(access.Path) != "" {
		return access.Error()
	}
	switch {
	case errors.Is(err, ErrBackupBusy):
		return "Файлы заняты, изменения не применены."
	case errors.Is(err, ErrBackupDenied):
		return ErrBackupDenied.Error()
	case errors.Is(err, ErrBackupCopy):
		return ErrBackupCopy.Error()
	case errors.Is(err, ErrNeedMilestone):
		return ErrNeedMilestone.Error()
	case errors.Is(err, ErrBackupRunning):
		return ErrBackupRunning.Error()
	case err != nil && strings.TrimSpace(err.Error()) != "":
		msg := err.Error()
		if strings.ContainsAny(msg, "АБВГДЕЖЗИКЛМНОПРСТУФХЦЧШЩЭЮЯабвгдежзиклмнопрстуфхцчшщэюя") {
			return msg
		}
	}
	return "Не удалось сделать бекап."
}

func (s *Service) scopeFor(label string) (string, error) {
	switch label {
	case labelManual, labelRestart, labelDaily, labelMilestone:
		raw, err := s.db.BackupScope()
		if err != nil {
			return "", err
		}
		return normalizeBackupScope(raw), nil
	default:
		return backupScopeFull, nil
	}
}

func (s *Service) scopeOf(id string) (string, error) {
	b, err := s.db.GetBackup(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", ErrNotFound
		}
		return "", err
	}
	if knownBackupScope(b.Scope) {
		return b.Scope, nil
	}
	root, err := s.backupRoot()
	if err != nil {
		return "", mapSnapshot(err)
	}
	if legacyBackup(root, id) {
		if isDir(filepath.Join(root, id, "mods")) || isDir(filepath.Join(root, id, "config")) {
			return backupScopeFull, nil
		}
		return backupScopeWorld, nil
	}
	man, err := chunkbackup.LoadManifest(chunkbackup.ManifestPath(root, id))
	if err != nil {
		return "", ErrBackupMissing
	}
	if knownBackupScope(man.Scope) {
		return man.Scope, nil
	}
	return backupScopeWorld, nil
}

func (s *Service) captureNow(label, kind, scope, id, parentID string) (store.Backup, error) {
	modsDir, configDir, worldDir, err := s.layout()
	if err != nil {
		return store.Backup{}, err
	}
	if scope == backupScopeFull || scope == backupScopeMods {
		modsDir, err = s.present(modsDir, true)
		if err != nil {
			return store.Backup{}, err
		}
		configDir, err = s.present(configDir, false)
		if err != nil {
			return store.Backup{}, err
		}
	} else {
		modsDir = ""
		configDir = ""
	}
	if scope == backupScopeMods {
		worldDir = ""
	} else {
		worldDir, err = s.present(worldDir, false)
		if err != nil {
			return store.Backup{}, err
		}
	}
	var pairs [][2]string
	if modsDir != "" {
		pairs = append(pairs, [2]string{modsDir, "mods"})
	}
	if configDir != "" {
		pairs = append(pairs, [2]string{configDir, "config"})
	}
	if worldDir != "" {
		pairs = append(pairs, [2]string{worldDir, "world"})
	}
	if len(pairs) == 0 {
		return store.Backup{}, ErrBackupCopy
	}
	var files, nbytes int64
	for _, pair := range pairs {
		n, b, err := measureDir(pair[0])
		if err != nil {
			return store.Backup{}, err
		}
		files += n
		nbytes += b
	}
	root, err := s.backupRoot()
	if err != nil {
		return store.Backup{}, mapSnapshot(err)
	}
	st, err := chunkbackup.Open(filepath.Join(root, "chunks"))
	if err != nil {
		return store.Backup{}, mapSnapshot(err)
	}
	var parent *chunkbackup.Manifest
	if kind == backupKindSnapshot {
		pm, err := chunkbackup.LoadManifest(chunkbackup.ManifestPath(root, parentID))
		if err != nil {
			return store.Backup{}, mapSnapshot(err)
		}
		parent = &pm
	}
	s.beginJob(files, nbytes, "копирование")
	defer s.endJob()
	note := &treeNote{bytes: s.addBytes, file: s.addFile}
	var entries []chunkbackup.FileEntry
	var roots []string
	for _, pair := range pairs {
		got, _, err := chunkbackup.Ingest(st, pair[0], pair[1], note.bytes, note.file)
		if err != nil {
			return store.Backup{}, mapSnapshot(err)
		}
		entries = append(entries, got...)
		roots = append(roots, pair[1])
	}
	man := chunkbackup.Manifest{
		Version: 1,
		ID:      id,
		Kind:    kind,
		Scope:   scope,
		Parent:  parentID,
		Roots:   roots,
		Files:   entries,
	}
	if parent != nil {
		man.Deleted = chunkbackup.DeletedPaths(parent.Files, entries)
	}
	sum, err := chunkbackup.ContentHash(man)
	if err != nil {
		return store.Backup{}, ErrBackupCopy
	}
	var skip map[string]struct{}
	if parent != nil {
		skip = chunkbackup.HashSet(parent.Files)
	}
	disk, err := st.DiskSize(entries, skip)
	if err != nil {
		return store.Backup{}, mapSnapshot(err)
	}
	if err := chunkbackup.SaveManifest(chunkbackup.ManifestPath(root, id), man); err != nil {
		return store.Backup{}, mapSnapshot(err)
	}
	return store.Backup{
		ID: id, CreatedAt: store.Now(), Label: label, Current: false,
		Kind: kind, Scope: scope, ParentID: parentID, Size: disk, SHA256: sum, Storage: "disk",
	}, nil
}

func (s *Service) exportBackup(id, dest string) error {
	root, err := s.backupRoot()
	if err != nil {
		return err
	}
	if legacyBackup(root, id) {
		return copyTree(filepath.Join(root, id), dest, nil)
	}
	return chunkbackup.RestoreStored(root, id, dest)
}

func chunkRestorePairs(st *chunkbackup.Store, root, id, modsDir, configDir, worldDir string) ([]dirPair, error) {
	man, err := chunkbackup.LoadManifest(chunkbackup.ManifestPath(root, id))
	if err != nil {
		return nil, ErrRollbackFailed
	}
	if man.Parent != "" {
		parent, err := chunkbackup.LoadManifest(chunkbackup.ManifestPath(root, man.Parent))
		if err != nil {
			return nil, ErrRollbackFailed
		}
		man = chunkbackup.Apply(parent, man)
	}
	if st == nil {
		return nil, ErrRollbackFailed
	}
	tops := map[string]struct{}{}
	for _, name := range man.Roots {
		tops[name] = struct{}{}
	}
	for _, f := range man.Files {
		top, _, _ := strings.Cut(f.Path, "/")
		tops[top] = struct{}{}
	}
	var pairs []dirPair
	add := func(live, name string) {
		if live == "" {
			return
		}
		if _, ok := tops[name]; !ok {
			return
		}
		n, b := chunkbackup.PrefixStats(man.Files, name)
		manifest := man
		prefix := name
		pairs = append(pairs, dirPair{
			live:   live,
			token:  id,
			files:  n,
			nbytes: b,
			fill: func(dst string, note *treeNote) error {
				var onBytes func(int64)
				var onFile func()
				if note != nil {
					onBytes = note.bytes
					onFile = note.file
				}
				return chunkbackup.MaterializePrefix(st, manifest, prefix, dst, onBytes, onFile)
			},
		})
	}
	add(modsDir, "mods")
	add(configDir, "config")
	add(worldDir, "world")
	if len(pairs) == 0 {
		return nil, ErrRollbackFailed
	}
	return pairs, nil
}

func (s *Service) fingerprint(b store.Backup) (int64, string, error) {
	root, err := s.backupRoot()
	if err != nil {
		return 0, "", mapSnapshot(err)
	}
	size, sum := b.Size, b.SHA256
	if legacyBackup(root, b.ID) {
		if size >= 0 && sum != "" {
			return size, sum, nil
		}
		ds, dh, err := chunkbackup.MeasureDir(filepath.Join(root, b.ID))
		if err != nil {
			return 0, "", mapSnapshot(err)
		}
		if size < 0 {
			size = ds
		}
		if sum == "" {
			sum = dh
		}
		return size, sum, nil
	}
	path := chunkbackup.ManifestPath(root, b.ID)
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
		if size < 0 {
			size = 0
		}
		if sum == "" {
			dh, err := chunkbackup.ContentHash(chunkbackup.Manifest{})
			if err != nil {
				return 0, "", err
			}
			sum = dh
		}
		return size, sum, nil
	}
	if size >= 0 && sum != "" {
		return size, sum, nil
	}
	man, err := chunkbackup.LoadManifest(path)
	if err != nil {
		return 0, "", mapSnapshot(err)
	}
	if sum == "" {
		sum, err = chunkbackup.ContentHash(man)
		if err != nil {
			return 0, "", err
		}
	}
	if size < 0 {
		chunks, err := chunkbackup.Open(filepath.Join(root, "chunks"))
		if err != nil {
			return 0, "", mapSnapshot(err)
		}
		var skip map[string]struct{}
		if man.Parent != "" {
			parent, err := chunkbackup.LoadManifest(chunkbackup.ManifestPath(root, man.Parent))
			if err != nil {
				return 0, "", mapSnapshot(err)
			}
			skip = chunkbackup.HashSet(parent.Files)
		}
		size, err = chunks.DiskSize(man.Files, skip)
		if err != nil {
			return 0, "", mapSnapshot(err)
		}
	}
	return size, sum, nil
}

func legacyBackup(root, id string) bool {
	dir := filepath.Join(root, id)
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return false
	}
	return pathInside(root, dir)
}

func isDir(path string) bool {
	st, err := os.Lstat(path)
	return err == nil && st.IsDir() && st.Mode()&os.ModeSymlink == 0
}

func measurePair(pair dirPair) (int64, int64, error) {
	if pair.fill != nil {
		return pair.files, pair.nbytes, nil
	}
	return measureDir(pair.src)
}
