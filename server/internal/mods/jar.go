package mods

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"strings"
)

type Meta struct {
	Slug        string
	Name        string
	Description string
	Version     string
}

func validSlug(s string) bool {
	if len(s) < 1 || len(s) > 128 {
		return false
	}
	return !strings.ContainsAny(s, "/\\\x00\r\n")
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func ReadMeta(f *os.File, filename string) (Meta, error) {
	stem := strings.TrimSuffix(filename, ".jar")
	fb := Meta{Slug: stem, Name: stem}
	st, err := f.Stat()
	if err != nil {
		return Meta{}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Meta{}, err
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil || st.Size() == 0 {
		return fb, nil
	}
	m, ok, err := metaFromZip(zr)
	if err != nil {
		return Meta{}, err
	}
	if !ok {
		return fb, nil
	}
	m.Slug = strings.TrimSpace(m.Slug)
	if !validSlug(m.Slug) {
		m.Slug = stem
	}
	m.Name = truncateRunes(strings.TrimSpace(m.Name), 200)
	m.Description = truncateRunes(m.Description, 4000)
	m.Version = truncateRunes(strings.TrimSpace(m.Version), 64)
	if m.Name == "" {
		m.Name = m.Slug
	}
	return m, nil
}

func metaFromZip(zr *zip.Reader) (Meta, bool, error) {
	if f := zipEntry(zr, "fabric.mod.json"); f != nil {
		return parseModJSON(f, false)
	}
	if f := zipEntry(zr, "quilt.mod.json"); f != nil {
		return parseModJSON(f, true)
	}
	if f := zipEntry(zr, "META-INF/mods.toml"); f != nil {
		b, err := readZipEntry(f)
		if err != nil {
			return Meta{}, false, err
		}
		m, err := parseModsToml(b)
		if err != nil {
			return Meta{}, false, nil
		}
		return m, true, nil
	}
	return Meta{}, false, nil
}

func zipEntry(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, 1<<20))
}

func parseModJSON(f *zip.File, quilt bool) (Meta, bool, error) {
	b, err := readZipEntry(f)
	if err != nil {
		return Meta{}, false, err
	}
	if quilt {
		m, err := parseQuilt(b)
		if err != nil {
			return Meta{}, false, nil
		}
		return m, true, nil
	}
	m, err := parseFabric(b)
	if err != nil {
		return Meta{}, false, nil
	}
	return m, true, nil
}

func parseFabric(b []byte) (Meta, error) {
	var raw struct {
		ID          string          `json:"id"`
		Version     string          `json:"version"`
		Name        string          `json:"name"`
		Description json.RawMessage `json:"description"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return Meta{}, err
	}
	return Meta{
		Slug:        raw.ID,
		Version:     raw.Version,
		Name:        raw.Name,
		Description: parseDescription(raw.Description),
	}, nil
}

func parseQuilt(b []byte) (Meta, error) {
	var raw struct {
		ID          string          `json:"id"`
		Version     string          `json:"version"`
		Name        string          `json:"name"`
		Description json.RawMessage `json:"description"`
		QuiltLoader struct {
			ID       string `json:"id"`
			Version  string `json:"version"`
			Metadata struct {
				Name        string          `json:"name"`
				Description json.RawMessage `json:"description"`
			} `json:"metadata"`
		} `json:"quilt_loader"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return Meta{}, err
	}
	m := Meta{
		Slug:        raw.ID,
		Version:     raw.Version,
		Name:        raw.Name,
		Description: parseDescription(raw.Description),
	}
	if id := strings.TrimSpace(raw.QuiltLoader.ID); id != "" {
		m.Slug = raw.QuiltLoader.ID
	}
	if raw.QuiltLoader.Version != "" {
		m.Version = raw.QuiltLoader.Version
	}
	if raw.QuiltLoader.Metadata.Name != "" {
		m.Name = raw.QuiltLoader.Metadata.Name
	}
	if len(raw.QuiltLoader.Metadata.Description) > 0 {
		m.Description = parseDescription(raw.QuiltLoader.Metadata.Description)
	}
	return m, nil
}

func parseDescription(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		return strings.Join(arr, "\n")
	}
	return ""
}

func parseModsToml(b []byte) (Meta, error) {
	text := string(b)
	i := strings.Index(text, "[[mods]]")
	if i < 0 {
		return Meta{}, os.ErrInvalid
	}
	rest := text[i+len("[[mods]]"):]
	if j := strings.Index(rest, "[["); j >= 0 {
		rest = rest[:j]
	}
	fields := map[string]string{}
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := splitTOML(line)
		if !ok {
			continue
		}
		if _, exists := fields[k]; !exists {
			fields[k] = v
		}
	}
	if fields["modId"] == "" && fields["version"] == "" && fields["displayName"] == "" && fields["description"] == "" {
		return Meta{}, os.ErrInvalid
	}
	return Meta{
		Slug:        fields["modId"],
		Version:     fields["version"],
		Name:        fields["displayName"],
		Description: fields["description"],
	}, nil
}

func splitTOML(line string) (string, string, bool) {
	eq := strings.IndexByte(line, '=')
	if eq <= 0 {
		return "", "", false
	}
	k := strings.TrimSpace(line[:eq])
	v := strings.TrimSpace(line[eq+1:])
	if k == "" {
		return "", "", false
	}
	return k, parseTOMLValue(v), true
}

func parseTOMLValue(v string) string {
	if strings.HasPrefix(v, "\"\"\"") || strings.HasPrefix(v, "'''") {
		return ""
	}
	if strings.HasPrefix(v, "\"") {
		return parseBasicString(v)
	}
	if strings.HasPrefix(v, "'") {
		return parseLiteralString(v)
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

func parseBasicString(v string) string {
	var b strings.Builder
	esc := false
	for i, r := range v[1:] {
		if esc {
			switch r {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '\\', '"':
				b.WriteRune(r)
			default:
				b.WriteRune(r)
			}
			esc = false
			continue
		}
		if r == '\\' {
			esc = true
			continue
		}
		if r == '"' {
			return b.String()
		}
		_ = i
		b.WriteRune(r)
	}
	return b.String()
}

func parseLiteralString(v string) string {
	v = v[1:]
	if i := strings.IndexByte(v, '\''); i >= 0 {
		return v[:i]
	}
	return v
}
