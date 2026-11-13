package mods

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"modupdater/internal/store"
)

type ChangelogDay struct {
	Day   string   `json:"day"`
	Lines []string `json:"lines"`
}

func (s *Service) Changelogs() ([]ChangelogDay, error) {
	events, err := s.db.ListChangelogEvents()
	if err != nil {
		return nil, err
	}
	return groupChangelog(events), nil
}

func (s *Service) GenerateChangelogs() ([]ChangelogDay, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	events, files, replaceFiles, err := s.changelogEvents()
	if err != nil {
		return nil, err
	}
	if err := s.db.SaveChangelog(events, files, replaceFiles); err != nil {
		return nil, err
	}
	return s.Changelogs()
}

func (s *Service) changelogEvents() ([]store.ChangelogEvent, []store.ChangelogFile, bool, error) {
	var out []store.ChangelogEvent
	hist, err := s.db.ListChangelogHistory()
	if err != nil {
		return nil, nil, false, err
	}
	for _, h := range hist {
		day, sortAt, ok := changelogTime(h.CreatedAt)
		if !ok {
			continue
		}
		name := changelogName(h.Name, h.Filename)
		if name == "" {
			continue
		}
		verb := "добавлен"
		if h.Action == "deleted" {
			verb = "удалён"
		}
		out = append(out, store.ChangelogEvent{
			ID: "h" + h.ID, Day: day, SortAt: sortAt, Line: verb + " мод " + name,
		})
	}
	backups, err := s.db.ListBackups()
	if err != nil {
		return nil, nil, false, err
	}
	for _, b := range backups {
		if b.Kind != backupKindMilestone || b.Status == "running" || b.Error != "" {
			continue
		}
		day, sortAt, ok := changelogTime(b.CreatedAt)
		if !ok {
			continue
		}
		when := ""
		if t, err := time.Parse(time.RFC3339, sortAt); err == nil {
			when = t.In(time.Local).Format("15:04")
		}
		line := "сделан бэкап вехи"
		if when != "" {
			line += " " + when
		}
		out = append(out, store.ChangelogEvent{
			ID: "b" + b.ID, Day: day, SortAt: sortAt, Line: line,
		})
	}
	fileEvents, files, replaceFiles, err := s.fileChangelog(time.Now())
	if err != nil {
		return nil, nil, false, err
	}
	out = append(out, fileEvents...)
	return out, files, replaceFiles, nil
}

func (s *Service) fileChangelog(now time.Time) ([]store.ChangelogEvent, []store.ChangelogFile, bool, error) {
	seeded, err := s.db.ChangelogSeeded()
	if err != nil {
		return nil, nil, false, err
	}
	prev, err := s.db.ListChangelogFiles()
	if err != nil {
		return nil, nil, false, err
	}
	seen := map[string]struct{}{}
	for _, f := range prev {
		seen[f.Kind+"\x00"+f.Name] = struct{}{}
	}
	cur, ok, err := s.scanChangelogFiles()
	if err != nil || !ok {
		return nil, nil, false, err
	}
	files := make([]store.ChangelogFile, 0, len(cur))
	for _, f := range cur {
		files = append(files, store.ChangelogFile{Kind: f.kind, Name: f.name})
	}
	if !seeded {
		return nil, files, true, nil
	}
	nowDay := now.In(time.Local).Format("2006-01-02")
	nowSort := now.UTC().Truncate(time.Second).Format(time.RFC3339)
	var out []store.ChangelogEvent
	curSet := map[string]struct{}{}
	for _, f := range cur {
		key := f.kind + "\x00" + f.name
		curSet[key] = struct{}{}
		if _, ok := seen[key]; ok {
			continue
		}
		day := f.mtime.In(time.Local).Format("2006-01-02")
		sortAt := f.mtime.UTC().Truncate(time.Second).Format(time.RFC3339)
		out = append(out, store.ChangelogEvent{
			ID:     fileEventID(f.kind, "added", f.name, day),
			Day:    day,
			SortAt: sortAt,
			Line:   "добавлен " + kindWord(f.kind) + " " + f.name,
		})
	}
	for _, f := range prev {
		if _, ok := curSet[f.Kind+"\x00"+f.Name]; ok {
			continue
		}
		out = append(out, store.ChangelogEvent{
			ID:     fileEventID(f.Kind, "deleted", f.Name, nowDay),
			Day:    nowDay,
			SortAt: nowSort,
			Line:   "удалён " + kindWord(f.Kind) + " " + f.Name,
		})
	}
	return out, files, true, nil
}

type fileHit struct {
	kind  string
	name  string
	mtime time.Time
}

func (s *Service) scanChangelogFiles() ([]fileHit, bool, error) {
	modsDir, err := s.resolve()
	if err != nil {
		if errors.Is(err, ErrModsDirUnset) || errors.Is(err, ErrInvalidModsDir) {
			return nil, false, nil
		}
		return nil, false, err
	}
	root := filepath.Dir(modsDir)
	plugins, err := listChangelogDir(filepath.Join(root, "plugins"), "plugin", "jar")
	if err != nil {
		return nil, false, err
	}
	packs, err := listChangelogDir(filepath.Join(root, "modpacks"), "modpack", "pack")
	if err != nil {
		return nil, false, err
	}
	world, err := s.datapackWorld()
	if err != nil {
		return nil, false, err
	}
	datapacks, err := listChangelogDir(filepath.Join(world, "datapacks"), "datapack", "datapack")
	if err != nil {
		return nil, false, err
	}
	return append(append(plugins, packs...), datapacks...), true, nil
}

func listChangelogDir(dir, kind, mode string) ([]fileHit, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []fileHit
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := e.Name()
		if name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "\r\n") {
			continue
		}
		if !changelogEntry(mode, name, e.IsDir()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, fileHit{kind: kind, name: name, mtime: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

func changelogEntry(mode, name string, dir bool) bool {
	low := strings.ToLower(name)
	switch mode {
	case "jar":
		return !dir && strings.HasSuffix(low, ".jar")
	case "datapack":
		return dir || strings.HasSuffix(low, ".zip")
	default:
		return dir || strings.HasSuffix(low, ".zip") || strings.HasSuffix(low, ".mrpack") || strings.HasSuffix(low, ".jar")
	}
}

func kindWord(kind string) string {
	switch kind {
	case "plugin":
		return "плагин"
	case "datapack":
		return "датапак"
	default:
		return "модпак"
	}
}

func changelogName(name, filename string) string {
	name = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(name, "\n", " "), "\r", " "))
	if name != "" {
		return name
	}
	return strings.TrimSpace(filename)
}

func changelogTime(raw string) (string, string, bool) {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return "", "", false
	}
	local := t.In(time.Local)
	return local.Format("2006-01-02"), t.UTC().Truncate(time.Second).Format(time.RFC3339), true
}

func fileEventID(kind, action, name, day string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + action + "\x00" + name + "\x00" + day))
	return hex.EncodeToString(sum[:])
}

func groupChangelog(events []store.ChangelogEvent) []ChangelogDay {
	if len(events) == 0 {
		return []ChangelogDay{}
	}
	order := []string{}
	by := map[string]*ChangelogDay{}
	for _, e := range events {
		day := by[e.Day]
		if day == nil {
			day = &ChangelogDay{Day: e.Day}
			by[e.Day] = day
			order = append(order, e.Day)
		}
		day.Lines = append(day.Lines, e.Line)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(order)))
	out := make([]ChangelogDay, 0, len(order))
	for _, day := range order {
		out = append(out, *by[day])
	}
	return out
}
