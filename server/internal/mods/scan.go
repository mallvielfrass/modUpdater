package mods

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"modupdater/internal/store"
)

func emptyScan(dir string) ScanResult {
	return ScanResult{ModsDir: dir, Conflicts: []string{}, Skipped: []string{}}
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
