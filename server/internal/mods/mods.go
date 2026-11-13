package mods

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"modupdater/internal/s3store"
	"modupdater/internal/store"
)

const (
	MaxJar     int64 = 256 << 20
	MaxConfig  int64 = 64 << 20
	MaxRelease int64 = 250 << 20
)

var (
	ErrInvalidModsDir  = errors.New("invalid_mods_dir")
	ErrModsDirUnset    = errors.New("mods_dir_unset")
	ErrInvalidFilename = errors.New("invalid_filename")
	ErrConflict        = errors.New("conflict")
	ErrSlugExists      = errors.New("slug_exists")
	ErrNotFound        = errors.New("not_found")
	ErrInvalidJSON     = errors.New("invalid_json")
	ErrPayloadTooLarge = errors.New("payload_too_large")
	ErrInvalidLimit    = errors.New("invalid_limit")
	ErrForbidden       = errors.New("forbidden")
	ErrInvalidDest     = errors.New("invalid_dest")
	ErrInvalidVersion  = errors.New("invalid_version")
	ErrJarRead         = errors.New("Не удалось прочитать файл мода.")
)

type ModAccessError struct {
	Path string
	File bool
}

func (e *ModAccessError) Error() string {
	kind := "каталогу"
	if e != nil && e.File {
		kind = "файлу"
	}
	path := ""
	if e != nil {
		path = e.Path
	}
	return "Нет доступа к " + kind + " " + path + "."
}

type ModWriteError struct {
	Path string
}

func (e *ModWriteError) Error() string {
	path := ""
	if e != nil {
		path = e.Path
	}
	return "Не удалось записать файл " + path + "."
}

type ScanResult struct {
	ModsDir   string   `json:"mods_dir"`
	Added     int      `json:"added"`
	Updated   int      `json:"updated"`
	Removed   int      `json:"removed"`
	Conflicts []string `json:"conflicts"`
	Skipped   []string `json:"skipped"`
}

type Event struct {
	ID        string `json:"id"`
	ModID     string `json:"mod_id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Action    string `json:"action"`
	Filename  string `json:"filename"`
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Client    bool   `json:"client"`
	CreatedAt string `json:"created_at"`
	HasFile   bool   `json:"has_file"`
}

type Upload struct {
	Filename       string
	File           io.ReadCloser
	Name           *string
	Description    *string
	Client         *bool
	ClientOnly     *bool
	ClientRequired *bool
}

type Service struct {
	db           *store.DB
	dataDir      string
	mu           sync.Mutex
	prog         progressSlot
	serviceStart func() (time.Time, bool)
	holdWorld    func(func() error) error
	s3Build      func(store.S3Settings) (s3store.Blob, error)
}

func New(db *store.DB, dataDir string) *Service {
	return &Service{db: db, dataDir: dataDir}
}

func emptyScan(dir string) ScanResult {
	return ScanResult{ModsDir: dir, Conflicts: []string{}, Skipped: []string{}}
}

func (s *Service) SetModsDir(raw string) (ScanResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if raw == "" {
		if err := s.db.SetModsDir(""); err != nil {
			return ScanResult{}, err
		}
		return emptyScan(""), nil
	}
	resolved, err := ResolveModsDir(s.dataDir, raw)
	if err != nil {
		return ScanResult{}, ErrInvalidModsDir
	}
	if err := s.db.SetModsDir(resolved); err != nil {
		return ScanResult{}, err
	}
	return s.scan()
}

func (s *Service) Scan() (ScanResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scan()
}

func (s *Service) resolve() (string, error) {
	raw, err := s.db.ModsDir()
	if err != nil {
		return "", err
	}
	if raw == "" {
		return "", ErrModsDirUnset
	}
	dir, err := ResolveModsDir(s.dataDir, raw)
	if err != nil {
		return "", ErrInvalidModsDir
	}
	return dir, nil
}

func (s *Service) archiveDir() (string, error) {
	return s.dataSubdir("archive")
}

func (s *Service) clientModsDir() (string, error) {
	return s.dataSubdir("client_mods")
}

func (s *Service) revokedDir() (string, error) {
	return s.dataSubdir("revoked")
}

func (s *Service) modDir(clientOnly bool) (string, error) {
	if clientOnly {
		return s.clientModsDir()
	}
	return s.resolve()
}

func (s *Service) dataSubdir(name string) (string, error) {
	if err := os.MkdirAll(s.dataDir, 0o755); err != nil {
		return "", err
	}
	dataAbs, err := filepath.Abs(s.dataDir)
	if err != nil {
		return "", err
	}
	dataReal, err := filepath.EvalSymlinks(dataAbs)
	if err != nil {
		return "", err
	}
	dataReal = filepath.Clean(dataReal)
	dir := filepath.Join(dataReal, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	real = filepath.Clean(real)
	if !pathInside(dataReal, real) {
		return "", ErrBadPath
	}
	return real, nil
}

type scanned struct {
	name string
	meta Meta
	sum  string
	size int64
}

func (s *Service) scan() (ScanResult, error) {
	dir, err := s.resolve()
	if err != nil {
		return ScanResult{}, err
	}
	arch, err := s.archiveDir()
	if err != nil {
		return ScanResult{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ScanResult{}, ErrInvalidModsDir
	}
	var files []scanned
	var skipped []string
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() {
			continue
		}
		safe, err := SafeJarName(name)
		if err != nil || safe != name {
			continue
		}
		fi, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			skipped = append(skipped, name)
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
			continue
		}
		if fi.Size() == 0 {
			skipped = append(skipped, name)
			continue
		}
		sum, size, meta, err := readLiveJar(dir, name)
		if err != nil {
			skipped = append(skipped, name)
			continue
		}
		if !validSlug(meta.Slug) {
			skipped = append(skipped, name)
			continue
		}
		if meta.Name == "" {
			meta.Name = meta.Slug
		}
		files = append(files, scanned{name: name, meta: meta, sum: sum, size: size})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })

	bySlug := map[string][]scanned{}
	for _, f := range files {
		bySlug[f.meta.Slug] = append(bySlug[f.meta.Slug], f)
	}
	conflict := map[string]bool{}
	conflictSlugs := map[string]bool{}
	var conflicts []string
	for slug, group := range bySlug {
		if len(group) < 2 {
			continue
		}
		conflictSlugs[slug] = true
		for _, f := range group {
			if !conflict[f.name] {
				conflict[f.name] = true
				conflicts = append(conflicts, f.name)
			}
		}
		delete(bySlug, slug)
	}
	rows, err := s.db.ListMods(false)
	if err != nil {
		return ScanResult{}, err
	}
	bySlugRow := map[string]store.Mod{}
	byNameRow := map[string]store.Mod{}
	clientSlug := map[string]bool{}
	clientName := map[string]bool{}
	for _, m := range rows {
		if m.ClientOnly || m.Revoked {
			clientSlug[m.Slug] = true
			clientName[m.Filename] = true
			continue
		}
		bySlugRow[m.Slug] = m
		byNameRow[m.Filename] = m
	}
	accepted := map[string]scanned{}
	for slug, group := range bySlug {
		f := group[0]
		if row, ok := byNameRow[f.name]; ok && row.Slug != slug {
			if !conflict[f.name] {
				conflict[f.name] = true
				conflicts = append(conflicts, f.name)
			}
			continue
		}
		accepted[slug] = f
	}
	res := emptyScan(dir)
	res.Skipped = skipped
	seen := map[string]bool{}
	for slug, f := range accepted {
		if clientSlug[slug] || clientName[f.name] {
			if !conflict[f.name] {
				conflict[f.name] = true
				conflicts = append(conflicts, f.name)
			}
			continue
		}
		seen[slug] = true
		row, ok := bySlugRow[slug]
		if !ok {
			if err := s.insertFile(f); err != nil {
				return ScanResult{}, err
			}
			res.Added++
			continue
		}
		if row.SHA256 == f.sum {
			if row.Filename != f.name || row.Version != f.meta.Version || row.Size != f.size {
				if err := s.db.UpdateModSame(row.ID, f.name, f.meta.Version, f.size, store.Now()); err != nil {
					return ScanResult{}, err
				}
			}
			continue
		}
		archName := ""
		if row.Filename != f.name && !conflict[row.Filename] {
			if sum, err := hashRegular(dir, row.Filename); err == nil && sum == row.SHA256 {
				hid, err := store.NewID()
				if err != nil {
					return ScanResult{}, err
				}
				archName = hid + ".jar"
				if err := relocate(dir, row.Filename, arch, archName); err != nil {
					return ScanResult{}, err
				}
			}
		}
		row.Filename = f.name
		row.SHA256 = f.sum
		row.Size = f.size
		row.Version = f.meta.Version
		row.UpdatedAt = store.Now()
		h := historyFrom(row, "updated", archName)
		if archName != "" {
			h.ID = strings.TrimSuffix(archName, ".jar")
		}
		if err := s.db.UpdateModFile(row, h); err != nil {
			return ScanResult{}, err
		}
		res.Updated++
	}
	for _, row := range rows {
		if row.ClientOnly || row.Revoked {
			continue
		}
		if seen[row.Slug] || conflictSlugs[row.Slug] || conflict[row.Filename] {
			continue
		}
		if filePresent(dir, row.Filename) {
			continue
		}
		h := historyFrom(row, "deleted", "")
		if err := s.deleteRow(row, h); err != nil {
			return ScanResult{}, err
		}
		res.Removed++
	}
	sort.Strings(conflicts)
	sort.Strings(res.Skipped)
	if conflicts == nil {
		conflicts = []string{}
	}
	if res.Skipped == nil {
		res.Skipped = []string{}
	}
	res.Conflicts = conflicts
	return res, nil
}

func filePresent(dir, name string) bool {
	safe, err := SafeJarName(name)
	if err != nil || safe != name {
		return true
	}
	_, err = os.Lstat(filepath.Join(dir, safe))
	return err == nil
}

func (s *Service) insertFile(f scanned) error {
	id, err := store.NewID()
	if err != nil {
		return err
	}
	now := store.Now()
	name := f.meta.Name
	if name == "" {
		name = f.meta.Slug
	}
	m := store.Mod{
		ID: id, Slug: f.meta.Slug, Name: name, Description: f.meta.Description,
		Filename: f.name, SHA256: f.sum, Size: f.size, Version: f.meta.Version,
		Client: false, ClientOnly: false, UpdatedAt: now, CreatedAt: now,
	}
	h := historyFrom(m, "added", "")
	return s.db.InsertModWithHistory(m, h)
}

func historyFrom(m store.Mod, action, archive string) store.History {
	id, err := store.NewID()
	if err != nil {
		id = m.ID
	}
	if archive != "" {
		id = strings.TrimSuffix(archive, ".jar")
	}
	return store.History{
		ID: id, ModID: m.ID, Slug: m.Slug, Name: m.Name, Action: action,
		Filename: m.Filename, Version: m.Version, SHA256: m.SHA256, Size: m.Size,
		Client: m.Client, ClientOnly: m.ClientOnly, ArchiveName: archive, CreatedAt: store.Now(),
	}
}

func (s *Service) Add(up Upload) (store.Mod, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateText(up.Name, up.Description); err != nil {
		return store.Mod{}, err
	}
	clientOnly := up.ClientOnly != nil && *up.ClientOnly
	clientRequired := up.ClientRequired != nil && *up.ClientRequired
	var dir string
	var err error
	if clientOnly {
		dir, err = s.clientModsDir()
	} else {
		dir, err = s.resolve()
	}
	if err != nil {
		return store.Mod{}, err
	}
	if up.File == nil {
		return store.Mod{}, ErrInvalidJSON
	}
	safe, err := SafeJarName(up.Filename)
	if err != nil {
		return store.Mod{}, ErrInvalidFilename
	}
	if err := s.ensureFree(dir, safe, ""); err != nil {
		return store.Mod{}, err
	}
	tmp, sum, size, err := writeTemp(dir, up.File, MaxJar)
	if err != nil {
		return store.Mod{}, err
	}
	meta, err := metaFromPath(tmp, safe)
	if err != nil {
		os.Remove(tmp)
		return store.Mod{}, err
	}
	if !validSlug(meta.Slug) {
		os.Remove(tmp)
		return store.Mod{}, ErrInvalidFilename
	}
	if _, err := s.db.ModBySlug(meta.Slug); err == nil {
		os.Remove(tmp)
		return store.Mod{}, ErrSlugExists
	} else if !errors.Is(err, store.ErrNotFound) {
		os.Remove(tmp)
		return store.Mod{}, err
	}
	if err := commitTemp(tmp, dir, safe); err != nil {
		return store.Mod{}, err
	}
	name, desc, client := applyFields(meta, up.Name, up.Description, up.Client, false)
	if clientOnly {
		client = true
	}
	id, err := store.NewID()
	if err != nil {
		os.Remove(filepath.Join(dir, safe))
		return store.Mod{}, err
	}
	now := store.Now()
	m := store.Mod{
		ID: id, Slug: meta.Slug, Name: name, Description: desc, Filename: safe,
		SHA256: sum, Size: size, Version: meta.Version, Client: client, ClientOnly: clientOnly, ClientRequired: clientRequired, UpdatedAt: now, CreatedAt: now,
	}
	h := historyFrom(m, "added", "")
	if err := s.db.InsertModWithHistory(m, h); err != nil {
		os.Remove(filepath.Join(dir, safe))
		return store.Mod{}, err
	}
	return m, nil
}

func (s *Service) Update(id string, up Upload) (store.Mod, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !store.ValidID(id) {
		return store.Mod{}, ErrNotFound
	}
	if err := validateText(up.Name, up.Description); err != nil {
		return store.Mod{}, err
	}
	row, err := s.prepareMod(id)
	if err != nil {
		return store.Mod{}, err
	}
	if row.Revoked {
		return store.Mod{}, ErrConflict
	}
	dir, err := s.modDir(row.ClientOnly)
	if err != nil {
		return store.Mod{}, mapModPath(dir, false, err)
	}
	if up.File == nil {
		applyUpdate(&row, up)
		row.UpdatedAt = store.Now()
		if err := s.db.UpdateModMeta(row.ID, row.Name, row.Description, row.Client, row.ClientRequired, row.UpdatedAt); err != nil {
			return store.Mod{}, err
		}
		return row, nil
	}
	safe, err := SafeJarName(up.Filename)
	if err != nil {
		return store.Mod{}, ErrInvalidFilename
	}
	if safe != row.Filename {
		if err := s.ensureFree(dir, safe, row.ID); err != nil {
			return store.Mod{}, mapModPath(filepath.Join(dir, safe), true, err)
		}
	}
	tmp, sum, size, err := writeTemp(dir, up.File, MaxJar)
	if err != nil {
		return store.Mod{}, mapModPath(dir, false, err)
	}
	meta, err := metaFromPath(tmp, safe)
	if err != nil {
		os.Remove(tmp)
		if osErr(err) {
			return store.Mod{}, mapModPath(tmp, true, err)
		}
		return store.Mod{}, ErrJarRead
	}
	archName := ""
	if presentRegular(dir, row.Filename) {
		arch, aerr := s.archiveDir()
		if aerr != nil {
			os.Remove(tmp)
			return store.Mod{}, mapModPath(arch, false, aerr)
		}
		hid, aerr := store.NewID()
		if aerr != nil {
			os.Remove(tmp)
			return store.Mod{}, aerr
		}
		archName = hid + ".jar"
		if aerr = relocate(dir, row.Filename, arch, archName); aerr != nil {
			os.Remove(tmp)
			return store.Mod{}, mapModPath(filepath.Join(dir, row.Filename), true, aerr)
		}
	}
	if err := commitTemp(tmp, dir, safe); err != nil {
		if archName != "" {
			if arch, aerr := s.archiveDir(); aerr == nil {
				_ = moveJar(arch, archName, dir, row.Filename)
			}
		}
		return store.Mod{}, mapModPath(filepath.Join(dir, safe), true, err)
	}
	applyUpdate(&row, up)
	row.Filename = safe
	row.SHA256 = sum
	row.Size = size
	row.Version = meta.Version
	row.UpdatedAt = store.Now()
	h := historyFrom(row, "updated", archName)
	if err := s.db.UpdateModFile(row, h); err != nil {
		return store.Mod{}, err
	}
	return row, nil
}

func (s *Service) prepareMod(id string) (store.Mod, error) {
	if !store.ValidID(id) {
		return store.Mod{}, ErrNotFound
	}
	row, err := s.db.GetMod(id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.Mod{}, err
	}
	if err != nil || !row.ClientOnly {
		if _, rerr := s.resolve(); rerr != nil {
			return store.Mod{}, rerr
		}
	}
	if err != nil {
		return store.Mod{}, ErrNotFound
	}
	return row, nil
}

func applyUpdate(row *store.Mod, up Upload) {
	if up.Name != nil {
		row.Name = *up.Name
	}
	if up.Description != nil {
		row.Description = *up.Description
	}
	if up.Client != nil {
		row.Client = *up.Client
	}
	if up.ClientRequired != nil {
		row.ClientRequired = *up.ClientRequired
	}
	if row.ClientOnly {
		row.Client = true
	}
}

func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.prepareMod(id)
	if err != nil {
		return err
	}
	if row.Revoked {
		return s.deleteRevoked(row)
	}
	dir, err := s.modDir(row.ClientOnly)
	if err != nil {
		return err
	}
	archName := ""
	if presentRegular(dir, row.Filename) {
		arch, err := s.archiveDir()
		if err != nil {
			return err
		}
		hid, err := store.NewID()
		if err != nil {
			return err
		}
		archName = hid + ".jar"
		if err := relocate(dir, row.Filename, arch, archName); err != nil {
			return err
		}
	}
	h := historyFrom(row, "deleted", archName)
	return s.deleteRow(row, h)
}

func (s *Service) ensureFree(dir, name, selfID string) error {
	if owner, err := s.db.ModByFilename(name); err == nil {
		if owner.ID != selfID {
			return ErrConflict
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	path := filepath.Join(dir, name)
	if _, err := os.Lstat(path); err == nil {
		if selfID != "" {
			if owner, oerr := s.db.ModByFilename(name); oerr == nil && owner.ID == selfID {
				return nil
			}
		}
		return ErrConflict
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func validateText(name, desc *string) error {
	if name != nil {
		n := len([]rune(*name))
		if n < 1 || n > 200 {
			return ErrInvalidJSON
		}
	}
	if desc != nil && len([]rune(*desc)) > 4000 {
		return ErrInvalidJSON
	}
	return nil
}

func applyFields(meta Meta, name, desc *string, client *bool, fallbackClient bool) (string, string, bool) {
	n := meta.Name
	if name != nil {
		n = *name
	}
	if n == "" {
		n = meta.Slug
	}
	d := meta.Description
	if desc != nil {
		d = *desc
	}
	c := fallbackClient
	if client != nil {
		c = *client
	}
	return n, d, c
}

func (s *Service) StoredDir() (string, error) {
	return s.db.ModsDir()
}

func (s *Service) PublicList() ([]store.Mod, error) {
	return s.db.ListMods(true)
}

func (s *Service) PublicRevoked() ([]store.Mod, error) {
	return s.db.ListRevoked()
}

func (s *Service) Revoke(id string) (store.Mod, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.prepareMod(id)
	if err != nil {
		return store.Mod{}, err
	}
	if row.Revoked {
		return row, nil
	}
	live, err := s.modDir(row.ClientOnly)
	if err != nil {
		return store.Mod{}, err
	}
	stored, err := revokedFileName(row.ID)
	if err != nil {
		return store.Mod{}, err
	}
	hold, err := s.revokedDir()
	if err != nil {
		return store.Mod{}, err
	}
	if !presentRegular(live, row.Filename) {
		return store.Mod{}, ErrNotFound
	}
	if err := moveJar(live, row.Filename, hold, stored); err != nil {
		return store.Mod{}, err
	}
	row.Revoked = true
	row.UpdatedAt = store.Now()
	var hist *store.History
	if !row.ClientOnly {
		h := historyFrom(row, "deleted", "")
		hist = &h
	}
	if err := s.db.SetRevoked(row.ID, row.UpdatedAt, hist); err != nil {
		_ = moveJar(hold, stored, live, row.Filename)
		return store.Mod{}, err
	}
	return row, nil
}

func (s *Service) Restore(id string) (store.Mod, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, err := s.prepareMod(id)
	if err != nil {
		return store.Mod{}, err
	}
	if !row.Revoked {
		return row, nil
	}
	live, err := s.modDir(row.ClientOnly)
	if err != nil {
		return store.Mod{}, err
	}
	stored, err := revokedFileName(row.ID)
	if err != nil {
		return store.Mod{}, err
	}
	hold, err := s.revokedDir()
	if err != nil {
		return store.Mod{}, err
	}
	if !presentRegular(hold, stored) {
		return store.Mod{}, ErrNotFound
	}
	if presentRegular(live, row.Filename) {
		return store.Mod{}, ErrConflict
	}
	if owner, err := s.db.ModByFilename(row.Filename); err == nil && owner.ID != row.ID {
		return store.Mod{}, ErrConflict
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return store.Mod{}, err
	}
	if err := moveJar(hold, stored, live, row.Filename); err != nil {
		return store.Mod{}, err
	}
	row.Revoked = false
	row.UpdatedAt = store.Now()
	var hist *store.History
	if !row.ClientOnly {
		h := historyFrom(row, "added", "")
		hist = &h
	}
	if err := s.db.ClearRevoked(row.ID, row.UpdatedAt, hist); err != nil {
		_ = moveJar(live, row.Filename, hold, stored)
		return store.Mod{}, err
	}
	return row, nil
}

func (s *Service) deleteRevoked(row store.Mod) error {
	if err := s.removeRevokedFile(row.ID); err != nil {
		return err
	}
	cfgs, err := s.db.ListConfigs(row.ID)
	if err != nil {
		return err
	}
	if err := s.db.DeleteMod(row.ID); err != nil {
		return err
	}
	for _, c := range cfgs {
		if err := s.removeBlob(c.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) removeRevokedFile(id string) error {
	name, err := revokedFileName(id)
	if err != nil {
		return err
	}
	dir, err := s.revokedDir()
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(dir, name))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func revokedFileName(id string) (string, error) {
	if !store.ValidID(id) {
		return "", ErrBadPath
	}
	name := id + ".jar"
	if !SafeArchiveName(name) {
		return "", ErrBadPath
	}
	return name, nil
}

func (s *Service) ListAll() ([]store.Mod, error) {
	return s.db.ListMods(false)
}

func (s *Service) PublicGet(id string) (store.Mod, error) {
	if !store.ValidID(id) {
		return store.Mod{}, ErrNotFound
	}
	m, err := s.db.GetMod(id)
	if err != nil || !clientVisible(m) {
		return store.Mod{}, ErrNotFound
	}
	return m, nil
}

func (s *Service) OpenClientFile(id string) (*os.File, string, error) {
	if !store.ValidID(id) {
		return nil, "", ErrNotFound
	}
	m, err := s.db.GetMod(id)
	if err != nil || !clientVisible(m) {
		return nil, "", ErrNotFound
	}
	var dir string
	if m.ClientOnly {
		dir, err = s.clientModsDir()
	} else {
		dir, err = s.resolve()
	}
	if err != nil {
		return nil, "", ErrNotFound
	}
	safe, err := SafeJarName(m.Filename)
	if err != nil || safe != m.Filename {
		return nil, "", ErrNotFound
	}
	f, err := OpenRegular(dir, safe)
	if err != nil {
		return nil, "", ErrNotFound
	}
	return f, safe, nil
}

func (s *Service) OpenDownloadFile(id string) (*os.File, string, error) {
	if !store.ValidID(id) {
		return nil, "", ErrNotFound
	}
	m, err := s.db.GetMod(id)
	if err != nil || m.Revoked {
		return nil, "", ErrNotFound
	}
	dir, err := s.modDir(m.ClientOnly)
	if err != nil {
		return nil, "", ErrNotFound
	}
	safe, err := SafeJarName(m.Filename)
	if err != nil || safe != m.Filename {
		return nil, "", ErrNotFound
	}
	f, err := OpenRegular(dir, safe)
	if err != nil {
		return nil, "", ErrNotFound
	}
	return f, safe, nil
}

func clientVisible(m store.Mod) bool {
	return m.Client && !m.Revoked
}

func (s *Service) PublicHistory(limit int) ([]Event, error) {
	if limit < 1 || limit > 200 {
		return nil, ErrInvalidLimit
	}
	rows, err := s.db.ListPublicHistory(limit)
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, h := range rows {
		out = append(out, s.toEvent(h))
	}
	return out, nil
}

func (s *Service) OpenHistoryFile(id string) (*os.File, string, error) {
	if !store.ValidID(id) {
		return nil, "", ErrNotFound
	}
	h, err := s.db.GetHistory(id)
	if err != nil || !h.Client {
		return nil, "", ErrNotFound
	}
	m, err := s.db.GetMod(h.ModID)
	if err == nil && !m.Client {
		return nil, "", ErrNotFound
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, "", ErrNotFound
	}
	if !SafeArchiveName(h.ArchiveName) {
		return nil, "", ErrNotFound
	}
	arch, err := s.archiveDir()
	if err != nil {
		return nil, "", ErrNotFound
	}
	f, err := OpenRegular(arch, h.ArchiveName)
	if err != nil {
		return nil, "", ErrNotFound
	}
	safe, err := SafeJarName(h.Filename)
	if err != nil || safe != h.Filename {
		f.Close()
		return nil, "", ErrNotFound
	}
	return f, safe, nil
}

func (s *Service) toEvent(h store.History) Event {
	return Event{
		ID: h.ID, ModID: h.ModID, Slug: h.Slug, Name: h.Name, Action: h.Action,
		Filename: h.Filename, Version: h.Version, SHA256: h.SHA256, Size: h.Size,
		Client: h.Client, CreatedAt: h.CreatedAt, HasFile: s.historyHasFile(h.ArchiveName),
	}
}

func (s *Service) historyHasFile(name string) bool {
	if !SafeArchiveName(name) {
		return false
	}
	arch, err := s.archiveDir()
	if err != nil {
		return false
	}
	f, err := OpenRegular(arch, name)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func mapModPath(path string, file bool, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrBadPath) {
		return ErrDatapackPath
	}
	if !osErr(err) {
		return err
	}
	path = filepath.Clean(path)
	if strings.TrimSpace(path) == "" || path == "." {
		return err
	}
	if errors.Is(err, os.ErrPermission) {
		return &ModAccessError{Path: path, File: file}
	}
	return &ModWriteError{Path: path}
}

func osErr(err error) bool {
	var errno syscall.Errno
	var pe *os.PathError
	var le *os.LinkError
	return errors.As(err, &errno) || errors.As(err, &pe) || errors.As(err, &le)
}

func presentRegular(dir, name string) bool {
	safe, err := SafeJarName(name)
	if err != nil || safe != name {
		return false
	}
	f, err := OpenRegular(dir, safe)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func readLiveJar(dir, name string) (string, int64, Meta, error) {
	f, err := OpenRegular(dir, name)
	if err != nil {
		return "", 0, Meta{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, Meta{}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", 0, Meta{}, err
	}
	meta, err := ReadMeta(f, name)
	if err != nil {
		return "", 0, Meta{}, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, meta, nil
}

func hashRegular(dir, name string) (string, error) {
	safe, err := SafeJarName(name)
	if err != nil || safe != name {
		return "", ErrBadPath
	}
	f, err := OpenRegular(dir, safe)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeTemp(dir string, r io.Reader, limit int64) (string, string, int64, error) {
	f, err := os.CreateTemp(dir, ".part-*")
	if err != nil {
		return "", "", 0, err
	}
	tmp := f.Name()
	if strings.HasSuffix(tmp, ".jar") {
		f.Close()
		os.Remove(tmp)
		return "", "", 0, errors.New("temp suffix")
	}
	h := sha256.New()
	n, copyErr := io.Copy(f, io.TeeReader(io.LimitReader(r, limit+1), h))
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return "", "", 0, copyErr
	}
	if closeErr != nil {
		os.Remove(tmp)
		return "", "", 0, closeErr
	}
	if n > limit {
		os.Remove(tmp)
		return "", "", 0, ErrPayloadTooLarge
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return "", "", 0, err
	}
	return tmp, hex.EncodeToString(h.Sum(nil)), n, nil
}

func commitTemp(tmp, dir, name string) error {
	if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func moveJar(srcDir, srcName, dstDir, dstName string) error {
	srcSafe, err := SafeJarName(srcName)
	if err != nil || srcSafe != srcName {
		return ErrBadPath
	}
	dstSafe, err := SafeJarName(dstName)
	if err != nil || dstSafe != dstName {
		return ErrBadPath
	}
	if presentRegular(dstDir, dstName) {
		return ErrConflict
	}
	src := filepath.Join(srcDir, srcName)
	dst := filepath.Join(dstDir, dstName)
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyRegular(srcDir, srcName, dstDir, dstName); err != nil {
		return err
	}
	return os.Remove(src)
}

func relocate(srcDir, srcName, dstDir, dstName string) error {
	if !SafeArchiveName(dstName) {
		return ErrBadPath
	}
	safe, err := SafeJarName(srcName)
	if err != nil || safe != srcName {
		return ErrBadPath
	}
	src := filepath.Join(srcDir, srcName)
	dst := filepath.Join(dstDir, dstName)
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyRegular(srcDir, srcName, dstDir, dstName); err != nil {
		return err
	}
	return os.Remove(src)
}

func copyRegular(srcDir, srcName, dstDir, dstName string) error {
	in, err := OpenRegular(srcDir, srcName)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dstDir, ".part-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, err = io.Copy(tmp, in)
	cerr := tmp.Close()
	if err != nil || cerr != nil {
		os.Remove(tmpName)
		if err != nil {
			return err
		}
		return cerr
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dstDir, dstName)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
