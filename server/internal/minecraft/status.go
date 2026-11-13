package minecraft

import (
	"regexp"
	"strconv"
	"strings"
)

type LoadedMod struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	File    string `json:"file"`
}

type ServerStatus struct {
	Up         bool        `json:"up"`
	Online     int         `json:"online"`
	Max        int         `json:"max"`
	Names      []string    `json:"names"`
	PlayersRaw string      `json:"players_raw"`
	PlayersOK  bool        `json:"players_ok"`
	ModsOK     bool        `json:"mods_ok"`
	Mods       []LoadedMod `json:"mods"`
	ModsRaw    string      `json:"mods_raw"`
}

func (s *Service) Status() (ServerStatus, error) {
	st := ServerStatus{Names: []string{}, Mods: []LoadedMod{}}
	players, err := s.Command("list")
	if err != nil {
		return st, nil
	}
	st.Up = true
	st.PlayersRaw = players
	online, max, names, ok := ParseList(players)
	st.PlayersOK = ok
	if ok {
		st.Online = online
		st.Max = max
		if names != nil {
			st.Names = names
		}
	}
	mods, err := s.Command("forge mods")
	if err != nil {
		return st, nil
	}
	st.ModsOK = true
	st.ModsRaw = mods
	st.Mods = ParseMods(mods)
	return st, nil
}

var (
	listEN  = regexp.MustCompile(`(?i)^There are (\d+) of a max of (\d+) players online:\s*(.*)$`)
	listRU  = regexp.MustCompile(`(?i)^(?:В игре|Сейчас в игре) (\d+) из (\d+) игроков:\s*(.*)$`)
	modLine = regexp.MustCompile(`^[•\-\*\s]*(.*?)\s+:\s+(\S+)\s+\((.*?)\)\s+-\s+(\d+)\s*$`)
)

func ParseList(text string) (online, max int, names []string, ok bool) {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	m := listEN.FindStringSubmatch(text)
	if m == nil {
		m = listRU.FindStringSubmatch(text)
	}
	if m == nil {
		return 0, 0, []string{}, false
	}
	online, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, 0, []string{}, false
	}
	max, err = strconv.Atoi(m[2])
	if err != nil {
		return 0, 0, []string{}, false
	}
	return online, max, splitNames(m[3]), true
}

func splitNames(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ", ")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func ParseMods(text string) []LoadedMod {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	out := []LoadedMod{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		m := modLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out = append(out, LoadedMod{
			File:    strings.TrimSpace(m[1]),
			ID:      m[2],
			Version: m[3],
		})
	}
	return out
}
