package mods

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"modupdater/internal/store"
)

func TestSnapshotCopiesServerDirsOnly(t *testing.T) {
	db, svc, data, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=world\n")
	mustMkdir(t, filepath.Join(root, "config"))
	mustMkdir(t, filepath.Join(root, "world"))
	mustMkdir(t, filepath.Join(root, "logs"))
	mustMkdir(t, filepath.Join(root, "crash-reports"))
	mustMkdir(t, filepath.Join(root, "saves", "New World"))
	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "v1")
	writeServerFile(t, filepath.Join(root, "config", "c.txt"), "c1")
	writeServerFile(t, filepath.Join(root, "world", "w.txt"), "w1")
	writeServerFile(t, filepath.Join(root, "logs", "latest.log"), "LOGDATA")
	writeServerFile(t, filepath.Join(root, "crash-reports", "crash.txt"), "CRASH")
	writeServerFile(t, filepath.Join(root, "saves", "New World", "level.dat"), "CLIENTSAVE")
	if err := os.Symlink("a.txt", filepath.Join(modsDir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	useFullBackup(t, svc)
	first, err := svc.Milestone()
	if err != nil {
		t.Fatal(err)
	}
	if !first.Current || first.Label != "веха" || first.Kind != "milestone" || first.CreatedAt == "" || len(first.SHA256) != 64 || first.Size <= 0 {
		t.Fatalf("%+v", first)
	}
	snap := snapTree(t, svc, first.ID)
	if got := readServerFile(t, filepath.Join(snap, "mods", "a.txt")); got != "v1" {
		t.Fatal(got)
	}
	if got := readServerFile(t, filepath.Join(snap, "config", "c.txt")); got != "c1" {
		t.Fatal(got)
	}
	if got := readServerFile(t, filepath.Join(snap, "world", "w.txt")); got != "w1" {
		t.Fatal(got)
	}
	link, err := os.Readlink(filepath.Join(snap, "mods", "link.txt"))
	if err != nil || link != "a.txt" {
		t.Fatal(err, link)
	}
	if treeContains(t, snap, "LOGDATA") || treeContains(t, snap, "CRASH") || treeContains(t, snap, "CLIENTSAVE") {
		t.Fatal("snapshot includes excluded files")
	}
	st, err := os.Stat(filepath.Join(data, "backups"))
	if err != nil || st.Mode().Perm() != 0o755 {
		t.Fatal(st, err)
	}

	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "v2")
	writeServerFile(t, filepath.Join(root, "config", "c.txt"), "c2")
	writeServerFile(t, filepath.Join(root, "world", "w.txt"), "w2")
	list, _, pending, err := svc.Rollback(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readServerFile(t, filepath.Join(modsDir, "a.txt")) != "v1" || readServerFile(t, filepath.Join(root, "config", "c.txt")) != "c1" || readServerFile(t, filepath.Join(root, "world", "w.txt")) != "w1" {
		t.Fatal("rollback content")
	}
	if link, err = os.Readlink(filepath.Join(modsDir, "link.txt")); err != nil || link != "a.txt" {
		t.Fatal(err, link)
	}
	if len(list) != 2 {
		t.Fatal(len(list))
	}
	current := 0
	keptV2 := false
	for _, b := range list {
		if b.Current {
			current++
			if b.ID != first.ID {
				t.Fatal(b.ID)
			}
		} else if readServerFile(t, filepath.Join(snapTree(t, svc, b.ID), "mods", "a.txt")) == "v2" {
			keptV2 = true
			if b.Label != "перед откатом" {
				t.Fatal(b.Label)
			}
		}
	}
	if current != 1 || !keptV2 {
		t.Fatal(current, keptV2)
	}
	if len(pending) != 1 || pending[0].Name != "откат" {
		t.Fatalf("%+v", pending)
	}
	_ = db
}

func TestSnapshotLevelAndConfigPath(t *testing.T) {
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=NewWorld\nconfig-dir=custom-config\n")
	mustMkdir(t, filepath.Join(root, "config"))
	mustMkdir(t, filepath.Join(root, "custom-config"))
	mustMkdir(t, filepath.Join(root, "NewWorld"))
	writeServerFile(t, filepath.Join(root, "config", "c.txt"), "sib")
	writeServerFile(t, filepath.Join(root, "custom-config", "c.txt"), "other")
	writeServerFile(t, filepath.Join(root, "NewWorld", "w.txt"), "named")
	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "m")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	useFullBackup(t, svc)
	got, err := svc.Milestone()
	if err != nil {
		t.Fatal(err)
	}
	snap := snapTree(t, svc, got.ID)
	if readServerFile(t, filepath.Join(snap, "config", "c.txt")) != "sib" {
		t.Fatal("sibling config")
	}
	if readServerFile(t, filepath.Join(snap, "world", "w.txt")) != "named" {
		t.Fatal("level-name")
	}

	_, svc, _, modsDir = newMods(t)
	root = filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=world\nconfig-dir=custom-config\n")
	mustMkdir(t, filepath.Join(root, "custom-config"))
	mustMkdir(t, filepath.Join(root, "world"))
	writeServerFile(t, filepath.Join(root, "custom-config", "c.txt"), "other")
	writeServerFile(t, filepath.Join(root, "world", "w.txt"), "w")
	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "m")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	useFullBackup(t, svc)
	got, err = svc.Milestone()
	if err != nil {
		t.Fatal(err)
	}
	snap = snapTree(t, svc, got.ID)
	if _, err := os.Stat(filepath.Join(snap, "config")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if readServerFile(t, filepath.Join(snap, "world", "w.txt")) != "w" {
		t.Fatal("world")
	}
}

func TestSnapshotFindsRootAboveMods(t *testing.T) {
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	nested := filepath.Join(root, "game", "mods")
	mustMkdir(t, nested)
	mustMkdir(t, filepath.Join(root, "config"))
	mustMkdir(t, filepath.Join(root, "world"))
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=world\n")
	writeServerFile(t, filepath.Join(nested, "a.txt"), "deep")
	writeServerFile(t, filepath.Join(root, "config", "c.txt"), "rootcfg")
	writeServerFile(t, filepath.Join(root, "world", "w.txt"), "rootworld")
	if _, err := svc.SetModsDir(nested); err != nil {
		t.Fatal(err)
	}
	useFullBackup(t, svc)
	got, err := svc.Milestone()
	if err != nil {
		t.Fatal(err)
	}
	snap := snapTree(t, svc, got.ID)
	if readServerFile(t, filepath.Join(snap, "mods", "a.txt")) != "deep" {
		t.Fatal("mods")
	}
	if _, err := os.Stat(filepath.Join(snap, "config")); !os.IsNotExist(err) {
		t.Fatal("config is not next to mods")
	}
}

func TestRollbackBusyLeavesLiveFiles(t *testing.T) {
	_, svc, data, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=world\n")
	mustMkdir(t, filepath.Join(root, "config"))
	mustMkdir(t, filepath.Join(root, "world"))
	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "v1")
	writeServerFile(t, filepath.Join(root, "config", "c.txt"), "c1")
	writeServerFile(t, filepath.Join(root, "world", "w.txt"), "w1")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	useFullBackup(t, svc)
	first, err := svc.Milestone()
	if err != nil {
		t.Fatal(err)
	}
	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "v2")
	writeServerFile(t, filepath.Join(root, "config", "c.txt"), "c2")
	writeServerFile(t, filepath.Join(root, "world", "w.txt"), "w2")
	orig := renameFile
	t.Cleanup(func() { renameFile = orig })
	renameFile = func(from, to string) error {
		if filepath.Base(from) == "config" && strings.Contains(filepath.Base(to), ".prev-") {
			return syscall.EBUSY
		}
		return orig(from, to)
	}
	if _, _, _, err := svc.Rollback(first.ID); !errors.Is(err, ErrBackupBusy) {
		t.Fatal(err)
	}
	if readServerFile(t, filepath.Join(modsDir, "a.txt")) != "v2" || readServerFile(t, filepath.Join(root, "config", "c.txt")) != "c2" || readServerFile(t, filepath.Join(root, "world", "w.txt")) != "w2" {
		t.Fatal("mixed rollback")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		if strings.Contains(ent.Name(), ".stage-") || strings.Contains(ent.Name(), ".prev-") {
			t.Fatal(ent.Name())
		}
	}
	list, err := svc.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatal(len(list))
	}
	_ = data
}

func TestSnapshotHoldsWorldAroundCopy(t *testing.T) {
	_, svc, _, modsDir := newMods(t)
	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "v1")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	useFullBackup(t, svc)
	var marks []string
	svc.SetWorldHold(func(copy func() error) error {
		marks = append(marks, "begin")
		err := copy()
		marks = append(marks, "end")
		return err
	})
	if _, err := svc.Milestone(); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ManualSnapshot(); err != nil {
		t.Fatal(err)
	}
	if err := svc.SnapshotForRestart(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(marks, ",") != "begin,end,begin,end,begin,end" {
		t.Fatal(marks)
	}
}

func TestModsScopeSkipsWorldAndRCON(t *testing.T) {
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=world\n")
	mustMkdir(t, filepath.Join(root, "config"))
	mustMkdir(t, filepath.Join(root, "world"))
	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "mod")
	writeServerFile(t, filepath.Join(root, "config", "c.txt"), "cfg")
	writeServerFile(t, filepath.Join(root, "world", "w.txt"), "map")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc.SetWorldHold(func(copy func() error) error {
		calls++
		return copy()
	})
	if _, err := svc.SetBackupScope("world"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Milestone(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	if _, err := svc.SetBackupScope("mods"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ManualSnapshot(); !errors.Is(err, ErrNeedMilestone) {
		t.Fatal(err)
	}
	if err := svc.SnapshotForRestart(); !errors.Is(err, ErrNeedMilestone) {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	got, err := svc.Milestone()
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || got.Scope != "mods" || got.Kind != "milestone" {
		t.Fatal(calls, got.Scope, got.Kind)
	}
	snap := snapTree(t, svc, got.ID)
	if readServerFile(t, filepath.Join(snap, "mods", "a.txt")) != "mod" {
		t.Fatal("mods")
	}
	if readServerFile(t, filepath.Join(snap, "config", "c.txt")) != "cfg" {
		t.Fatal("config")
	}
	if _, err := os.Stat(filepath.Join(snap, "world")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	next, err := svc.ManualSnapshot()
	if err != nil || calls != 1 || next.Scope != "mods" || next.ParentID != got.ID {
		t.Fatal(calls, err, next.Scope, next.ParentID)
	}
}

func TestSnapshotSkipsMissingDirs(t *testing.T) {
	_, svc, _, modsDir := newMods(t)
	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "only")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	useFullBackup(t, svc)
	got, err := svc.Milestone()
	if err != nil {
		t.Fatal(err)
	}
	snap := snapTree(t, svc, got.ID)
	if readServerFile(t, filepath.Join(snap, "mods", "a.txt")) != "only" {
		t.Fatal("mods")
	}
	if _, err := os.Stat(filepath.Join(snap, "config")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(snap, "world")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := svc.SnapshotForRestart(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreBytesAndFree(t *testing.T) {
	root := t.TempDir()
	missing, err := storeBytes(filepath.Join(root, "backups"))
	if err != nil || missing != 0 {
		t.Fatal(missing, err)
	}
	chunks := filepath.Join(root, "backups", "chunks", "ab")
	if err := os.MkdirAll(chunks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chunks, "one"), bytes.Repeat([]byte("a"), 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "backups", "man.json"), bytes.Repeat([]byte("b"), 40), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.bin")
	if err := os.WriteFile(outside, bytes.Repeat([]byte("c"), 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "backups", "link.bin")); err != nil {
		t.Fatal(err)
	}
	got, err := storeBytes(filepath.Join(root, "backups"))
	if err != nil || got != 1040 {
		t.Fatal(got, err)
	}
	free, err := diskFree(root)
	if err != nil || free <= 0 {
		t.Fatal(free, err)
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(root, &st); err != nil {
		t.Fatal(err)
	}
	bsize := st.Frsize
	if bsize <= 0 {
		bsize = st.Bsize
	}
	want := int64(st.Bavail) * bsize
	if free != want {
		t.Fatal(free, want)
	}
	_, svc, _, _ := newMods(t)
	used, avail, err := svc.BackupDisk()
	if err != nil || used != 0 || avail <= 0 {
		t.Fatal(used, avail, err)
	}
}

func TestSnapshotPermissionMessage(t *testing.T) {
	if !errors.Is(mapSnapshot(os.ErrPermission), ErrBackupDenied) {
		t.Fatal(mapSnapshot(os.ErrPermission))
	}
	if mapSnapshot(errors.New("disk")) != ErrBackupCopy && !errors.Is(mapSnapshot(errors.New("disk")), ErrBackupCopy) {
		t.Fatal(mapSnapshot(errors.New("disk")))
	}
}

func TestWorldBackupSkipsUnreadableModsAndFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root")
	}
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	world := filepath.Join(root, "world")
	mustMkdir(t, world)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=world\n")
	writeServerFile(t, filepath.Join(world, "w.txt"), "map")
	hidden := filepath.Join(world, "secret.txt")
	writeServerFile(t, hidden, "nope")
	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "mod")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(modsDir, 0o755)
		_ = os.Chmod(world, 0o755)
		_ = os.Chmod(hidden, 0o644)
	})
	if err := os.Chmod(hidden, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(modsDir, 0); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Milestone()
	if err != nil {
		t.Fatal(err)
	}
	snap := snapTree(t, svc, got.ID)
	if readServerFile(t, filepath.Join(snap, "world", "w.txt")) != "map" {
		t.Fatal("world")
	}
	if _, err := os.Stat(filepath.Join(snap, "world", "secret.txt")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(snap, "mods")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Chmod(world, 0); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Milestone()
	if err == nil || !strings.Contains(err.Error(), world) || strings.Contains(err.Error(), "каталогу сервера") {
		t.Fatal(err)
	}
	var access *BackupAccessError
	if !errors.As(err, &access) || access.Path != world {
		t.Fatal(err)
	}
}

func TestDailyOncePerLocalDay(t *testing.T) {
	_, svc, _, modsDir := newMods(t)
	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "d")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	useFullBackup(t, svc)
	afternoon := time.Date(2026, 11, 5, 15, 0, 0, 0, time.Local)
	if _, err := svc.SetDailyTime("03:00"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DailyOnce(afternoon); !errors.Is(err, ErrNeedMilestone) {
		t.Fatal(err)
	}
	if _, err := svc.Milestone(); err != nil {
		t.Fatal(err)
	}
	if err := svc.DailyOnce(afternoon); err != nil {
		t.Fatal(err)
	}
	if err := svc.DailyOnce(afternoon); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListBackups()
	if err != nil || countLabel(list, "ежедневный") != 1 {
		t.Fatalf("%v %+v", err, list)
	}
	early := time.Date(2026, 11, 6, 2, 0, 0, 0, time.Local)
	if err := svc.DailyOnce(early); err != nil {
		t.Fatal(err)
	}
	list, err = svc.ListBackups()
	if err != nil || countLabel(list, "ежедневный") != 1 {
		t.Fatal(len(list), err)
	}
	if err := svc.DailyOnce(time.Date(2026, 11, 6, 3, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	list, err = svc.ListBackups()
	if err != nil || countLabel(list, "ежедневный") != 2 {
		t.Fatal(len(list), err)
	}
	if _, err := svc.SetDailyTime(""); err != nil {
		t.Fatal(err)
	}
	if err := svc.DailyOnce(time.Date(2026, 11, 6, 16, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	list, err = svc.ListBackups()
	if err != nil || countLabel(list, "ежедневный") != 2 {
		t.Fatal(len(list), err)
	}
	if _, err := svc.SetDailyTime("25:00"); !errors.Is(err, ErrBadBackupTime) {
		t.Fatal(err)
	}
}

func TestSnapshotProgressCountsFiles(t *testing.T) {
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=world\n")
	mustMkdir(t, filepath.Join(root, "config"))
	mustMkdir(t, filepath.Join(root, "world"))
	mustMkdir(t, filepath.Join(root, "logs"))
	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "v1")
	writeServerFile(t, filepath.Join(root, "config", "c.txt"), "c1")
	writeServerFile(t, filepath.Join(root, "world", "w.txt"), "w1")
	writeServerFile(t, filepath.Join(root, "logs", "latest.log"), "LOGDATA")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	useFullBackup(t, svc)
	if _, err := svc.Milestone(); err != nil {
		t.Fatal(err)
	}
	if readServerFile(t, filepath.Join(modsDir, "a.txt")) != "v1" || readServerFile(t, filepath.Join(root, "logs", "latest.log")) != "LOGDATA" {
		t.Fatal("live files")
	}
	p := svc.BackupProgress()
	if p.Running || p.Phase != "копирование" || p.FilesDone != 3 || p.FilesTotal != 3 || p.BytesDone != 6 || p.BytesTotal != 6 {
		t.Fatalf("%+v", p)
	}
}

func TestBackupZipSizeMatchesStream(t *testing.T) {
	_, svc, data, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	mustMkdir(t, filepath.Join(root, "config"))
	mustMkdir(t, filepath.Join(root, "world"))
	mustMkdir(t, filepath.Join(modsDir, "nested", "пусто"))
	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "mod")
	writeServerFile(t, filepath.Join(modsDir, "nested", "b.txt"), "bb")
	writeServerFile(t, filepath.Join(modsDir, "nested", "пусто", "мир.txt"), "привет")
	writeServerFile(t, filepath.Join(root, "config", "c.txt"), "")
	writeServerFile(t, filepath.Join(root, "world", "level.dat"), "world-bytes")
	if err := os.Symlink("a.txt", filepath.Join(modsDir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	useFullBackup(t, svc)
	id, err := store.NewID()
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(data, "backups", id)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(modsDir, filepath.Join(dest, "mods"), nil); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(root, "config"), filepath.Join(dest, "config"), nil); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(root, "world"), filepath.Join(dest, "world"), nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.AddBackup(store.Backup{ID: id, CreatedAt: store.Now(), Label: "ручной", Current: true}); err != nil {
		t.Fatal(err)
	}
	files := readBackupArchive(t, svc, id)
	if files["mods/a.txt"] != "mod" || files["mods/nested/b.txt"] != "bb" || files["mods/nested/пусто/мир.txt"] != "привет" || files["world/level.dat"] != "world-bytes" {
		t.Fatalf("%v", files)
	}
	if body, ok := files["config/c.txt"]; !ok || body != "" {
		t.Fatalf("%v", files)
	}
	for name := range files {
		if strings.Contains(name, "link") {
			t.Fatal(name)
		}
	}
	snap := filepath.Join(data, "backups", id)
	st, err := os.Stat(snap)
	if err != nil || !st.IsDir() {
		t.Fatal(st, err)
	}
	if err := os.RemoveAll(filepath.Join(snap, "mods")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(snap, "config")); err != nil {
		t.Fatal(err)
	}
	files = readBackupArchive(t, svc, id)
	if len(files) != 1 || files["world/level.dat"] != "world-bytes" {
		t.Fatalf("%v", files)
	}
	if err := os.RemoveAll(filepath.Join(snap, "world")); err != nil {
		t.Fatal(err)
	}
	files = readBackupArchive(t, svc, id)
	if len(files) != 0 {
		t.Fatalf("%v", files)
	}
}

func readBackupArchive(t *testing.T, svc *Service, id string) map[string]string {
	t.Helper()
	var buf bytes.Buffer
	if err := svc.WriteBackupZip(id, &buf); err != nil {
		t.Fatal(err)
	}
	plain, err := zstd.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	var raw bytes.Buffer
	if _, err := io.Copy(&raw, plain); err != nil {
		t.Fatal(err)
	}
	body := raw.Bytes()
	tr := tar.NewReader(bytes.NewReader(body))
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		file, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[hdr.Name] = string(file)
	}
	if len(out) > 0 && !bytes.Contains(body, []byte("ustar\x00")) {
		t.Fatal("tar magic")
	}
	return out
}

func TestBackupScopeWorldByDefault(t *testing.T) {
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=world\n")
	mustMkdir(t, filepath.Join(root, "config"))
	mustMkdir(t, filepath.Join(root, "world"))
	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "mod")
	writeServerFile(t, filepath.Join(root, "config", "c.txt"), "cfg")
	writeServerFile(t, filepath.Join(root, "world", "w.txt"), "map")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	scope, err := svc.BackupScope()
	if err != nil || scope != "world" {
		t.Fatal(scope, err)
	}
	var marks []string
	svc.SetWorldHold(func(copy func() error) error {
		marks = append(marks, "begin")
		err := copy()
		marks = append(marks, "end")
		return err
	})
	got, err := svc.Milestone()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(marks, ",") != "begin,end" {
		t.Fatal(marks)
	}
	assertWorldOnly(t, snapTree(t, svc, got.ID))

	if err := svc.db.SetBackupScope(""); err != nil {
		t.Fatal(err)
	}
	if scope, err = svc.BackupScope(); err != nil || scope != "world" {
		t.Fatal(scope, err)
	}
	if err := svc.db.SetBackupScope("bogus"); err != nil {
		t.Fatal(err)
	}
	if scope, err = svc.BackupScope(); err != nil || scope != "world" {
		t.Fatal(scope, err)
	}
	if _, err := svc.SetBackupScope("nope"); !errors.Is(err, ErrBadBackupScope) {
		t.Fatal(err)
	}
	saved, err := svc.SetBackupScope("")
	if err != nil || saved != "world" {
		t.Fatal(saved, err)
	}

	if _, err := svc.SetDailyTime("03:00"); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 11, 5, 4, 0, 0, 0, time.Local)
	if err := svc.DailyOnce(when); err != nil {
		t.Fatal(err)
	}
	if err := svc.SnapshotForRestart(); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	sawDaily, sawRestart := false, false
	for _, b := range list {
		switch b.Label {
		case "ежедневный":
			sawDaily = true
			assertWorldOnly(t, snapTree(t, svc, b.ID))
		case "перед перезапуском":
			sawRestart = true
			assertWorldOnly(t, snapTree(t, svc, b.ID))
		}
	}
	if !sawDaily || !sawRestart {
		t.Fatalf("%+v", list)
	}

	if _, err := svc.SetBackupScope("full"); err != nil {
		t.Fatal(err)
	}
	full, err := svc.Milestone()
	if err != nil {
		t.Fatal(err)
	}
	fullSnap := snapTree(t, svc, full.ID)
	if readServerFile(t, filepath.Join(fullSnap, "mods", "a.txt")) != "mod" || readServerFile(t, filepath.Join(fullSnap, "config", "c.txt")) != "cfg" || readServerFile(t, filepath.Join(fullSnap, "world", "w.txt")) != "map" {
		t.Fatal("full snapshot")
	}
	writeServerFile(t, filepath.Join(modsDir, "a.txt"), "live")
	if _, err := svc.SetBackupScope("world"); err != nil {
		t.Fatal(err)
	}
	rolled, _, _, err := svc.Rollback(full.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readServerFile(t, filepath.Join(modsDir, "a.txt")) != "mod" {
		t.Fatal("rollback")
	}
	sawSafety := false
	for _, b := range rolled {
		if b.Label == "перед откатом" {
			sawSafety = true
			if readServerFile(t, filepath.Join(snapTree(t, svc, b.ID), "mods", "a.txt")) != "live" {
				t.Fatal("safety scope")
			}
		}
	}
	if !sawSafety {
		t.Fatal(rolled)
	}
}

func assertWorldOnly(t *testing.T, snap string) {
	t.Helper()
	if readServerFile(t, filepath.Join(snap, "world", "w.txt")) != "map" {
		t.Fatal("world")
	}
	if _, err := os.Stat(filepath.Join(snap, "mods")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(snap, "config")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestMissingMilestoneDoesNotRepeatRCON(t *testing.T) {
	_, svc, _, modsDir := newMods(t)
	root := filepath.Dir(modsDir)
	writeServerFile(t, filepath.Join(root, "server.properties"), "level-name=world\n")
	mustMkdir(t, filepath.Join(root, "world"))
	writeServerFile(t, filepath.Join(root, "world", "w.txt"), "map")
	if _, err := svc.SetModsDir(modsDir); err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc.SetWorldHold(func(copy func() error) error {
		calls++
		return copy()
	})
	if _, err := svc.ManualSnapshot(); !errors.Is(err, ErrNeedMilestone) {
		t.Fatal(err)
	}
	if _, err := svc.ManualSnapshot(); !errors.Is(err, ErrNeedMilestone) {
		t.Fatal(err)
	}
	if err := svc.SnapshotForRestart(); !errors.Is(err, ErrNeedMilestone) {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal(calls)
	}
	if _, err := svc.Milestone(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	svc.SetWorldHold(func(copy func() error) error {
		calls++
		return errors.New("disk")
	})
	if _, err := svc.SetDailyTime("03:00"); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 11, 5, 4, 0, 0, 0, time.Local)
	if err := svc.DailyOnce(when); err == nil {
		t.Fatal("daily should fail")
	}
	if err := svc.DailyOnce(when); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}

func countLabel(list []store.Backup, label string) int {
	n := 0
	for _, b := range list {
		if b.Label == label {
			n++
		}
	}
	return n
}

func snapTree(t *testing.T, svc *Service, id string) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "export-"+id)
	if err := svc.exportBackup(id, dest); err != nil {
		t.Fatal(err)
	}
	return dest
}

func useFullBackup(t *testing.T, svc *Service) {
	t.Helper()
	if _, err := svc.SetBackupScope("full"); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeServerFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readServerFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func treeContains(t *testing.T, root, needle string) bool {
	t.Helper()
	found := false
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), needle) || strings.Contains(path, needle) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}
