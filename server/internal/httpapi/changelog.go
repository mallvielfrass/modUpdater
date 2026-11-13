package httpapi

import (
	"net/http"
	"strings"

	"modupdater/internal/mods"
)

type changelogPageDay struct {
	Day   string
	Lines []string
}

func (a *API) changelogPage(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	days, err := a.mods.Changelogs()
	if err != nil {
		writeMapped(w, err)
		return
	}
	page := make([]changelogPageDay, 0, len(days))
	for _, day := range days {
		page = append(page, changelogPageDay{Day: changelogDayLabel(day.Day), Lines: day.Lines})
	}
	writePage(w, "changelog", struct {
		Days []changelogPageDay
	}{Days: page})
}

func changelogDayLabel(day string) string {
	parts := strings.Split(day, "-")
	if len(parts) != 3 || len(parts[0]) != 4 || len(parts[1]) != 2 || len(parts[2]) != 2 {
		return day
	}
	return parts[2] + "." + parts[1] + "." + parts[0]
}

func (a *API) listChangelog(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	days, err := a.mods.Changelogs()
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeChangelog(w, days)
}

func (a *API) generateChangelog(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	days, err := a.mods.GenerateChangelogs()
	if err != nil {
		writeMapped(w, err)
		return
	}
	writeChangelog(w, days)
}

func writeChangelog(w http.ResponseWriter, days []mods.ChangelogDay) {
	if days == nil {
		days = []mods.ChangelogDay{}
	}
	writeJSON(w, http.StatusOK, struct {
		Days []mods.ChangelogDay `json:"days"`
	}{Days: days})
}
