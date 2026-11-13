package httpapi

import (
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"modupdater/internal/store"
)

var downloadPageTmpl = template.Must(template.New("download").Parse(`{{define "arrow"}}<svg xmlns="http://www.w3.org/2000/svg" width="22" height="22" viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M12 4v12" stroke="currentColor" stroke-width="2.4" stroke-linecap="round"/><path d="M6 12l6 7 6-7" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"/></svg>{{end}}
<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>modupdater</title>
<style>
:root { color-scheme: dark; }
* { box-sizing: border-box; }
body {
  margin: 0;
  font-family: "Segoe UI", system-ui, sans-serif;
  line-height: 1.4;
  color: #e7e8ea;
  background: #14161c;
}
.app { min-height: 100vh; padding: 20px 24px 32px; }
.appbar { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
.brand { font-size: 22px; font-weight: 700; letter-spacing: -0.03em; }
.nav { color: #3dd68c; font-weight: 650; text-decoration: none; }
.nav:hover { color: #8ef0c0; }
.card {
  background: #1c1f27;
  border: 1px solid #2c313c;
  border-radius: 10px;
  padding: 16px;
}
h2 { margin: 22px 0 8px; font-size: 16px; font-weight: 650; }
.card h2 { margin: 0 0 12px; }
.hint { color: #9aa0ab; margin: 8px 0; }
.ver, .filename { color: #9aa0ab; font-weight: 500; font-size: 13px; }
.downloads { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
.mod-downloads { margin-bottom: 12px; }
.btn {
  display: inline-block;
  border: 1px solid #2c313c;
  border-radius: 8px;
  background: #262a33;
  color: #e7e8ea;
  padding: 8px 14px;
  font-weight: 650;
  text-decoration: none;
}
.btn-primary { background: #3dd68c; border-color: #3dd68c; color: #10261b; }
.size { color: #9aa0ab; font-size: 13px; }
.sha { color: #9aa0ab; font-size: 12px; margin: 10px 0 0; word-break: break-all; }
.mod-list { display: flex; flex-direction: column; gap: 8px; }
.mod-card { display: flex; align-items: center; gap: 16px; padding: 12px 14px; }
.mod-head { display: flex; align-items: center; gap: 16px; }
.mod-main { min-width: 0; flex: 1; }
.dl {
  margin-left: auto;
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  color: #3dd68c;
  text-decoration: none;
  line-height: 0;
}
.dl:hover { color: #8ef0c0; }
.desc { margin: 12px 0 0; white-space: pre-wrap; }
.mod-title { display: flex; gap: 8px; align-items: baseline; flex-wrap: wrap; font-weight: 650; }
.mod-link { color: #3dd68c; text-decoration: underline; }
.mod-link:hover { color: #8ef0c0; }
.filename { margin: 2px 0 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.badge {
  border: 1px solid #2c313c;
  border-radius: 999px;
  color: #9aa0ab;
  font-size: 12px;
  font-weight: 650;
  padding: 2px 8px;
}
.badge-client { color: #10261b; background: #3dd68c; border-color: #3dd68c; }
</style>
</head>
<body>
<div class="app">
<header class="appbar"><div class="brand">modupdater{{if .HasRelease}} <span class="ver">v{{.Version}}</span>{{end}}</div><a class="nav" href="/changelog">Чейнжлог</a></header>
<section class="card">
<h2>Клиент</h2>
{{if .HasRelease}}
<p class="hint">Версия {{.Version}}</p>
<div class="downloads">
<a class="btn btn-primary" href="/api/client/releases/{{.Version}}/linux/file">Linux</a><span class="size">{{.LinuxSize}}</span>
<a class="btn btn-primary" href="/api/client/releases/{{.Version}}/windows/file">Windows</a><span class="size">{{.WindowsSize}}</span>
</div>
<p class="sha">{{.LinuxSHA256}}</p>
<p class="sha">{{.WindowsSHA256}}</p>
{{else}}
<p class="hint">Релиз ещё не опубликован.</p>
{{end}}
</section>
<h2>Список модов</h2>
<div class="downloads mod-downloads">
<a class="btn btn-primary" href="/api/mods/archive?kind=required">Архив обязательных клиентских модов</a>
<a class="btn btn-primary" href="/api/mods/archive?kind=optional">Архив необязательных клиентских модов</a>
<a class="btn btn-primary" href="/api/mods/archive?kind=all">Архив всех клиентских модов</a>
</div>
{{if .Mods}}
<div class="mod-list">
{{range .Mods}}
<article class="card mod-card">
<div class="mod-main">
<div class="mod-title"><a class="mod-link" href="/mods/{{.ID}}">{{.Name}}</a>{{if .Added}}<span class="ver">{{.Added}}</span>{{end}}{{if .ClientOnly}}<span class="badge badge-client">клиент</span>{{end}}{{if .Kind}}<span class="badge badge-client">{{.Kind}}</span>{{end}}<span class="ver">{{.Version}}</span></div>
<p class="filename">{{.Filename}}</p>
</div>
<a class="dl" href="/api/mods/{{.ID}}/download" aria-label="Скачать">{{template "arrow"}}</a>
</article>
{{end}}
</div>
{{else}}
<p class="hint">Список пуст.</p>
{{end}}
</div>
</body>
</html>
{{define "mod"}}<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Name}}</title>
<style>
:root { color-scheme: dark; }
* { box-sizing: border-box; }
body {
  margin: 0;
  font-family: "Segoe UI", system-ui, sans-serif;
  line-height: 1.4;
  color: #e7e8ea;
  background: #14161c;
}
.app { min-height: 100vh; padding: 20px 24px 32px; }
.appbar { display: flex; align-items: center; margin-bottom: 16px; }
.brand { font-size: 22px; font-weight: 700; letter-spacing: -0.03em; }
.card {
  background: #1c1f27;
  border: 1px solid #2c313c;
  border-radius: 10px;
  padding: 16px;
}
.ver, .filename { color: #9aa0ab; font-weight: 500; font-size: 13px; }
.mod-head { display: flex; align-items: center; gap: 16px; }
.mod-main { min-width: 0; flex: 1; }
.dl {
  margin-left: auto;
  flex: none;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  color: #3dd68c;
  text-decoration: none;
  line-height: 0;
}
.dl:hover { color: #8ef0c0; }
.desc { margin: 12px 0 0; white-space: pre-wrap; }
.mod-title { display: flex; gap: 8px; align-items: baseline; flex-wrap: wrap; font-weight: 650; }
.filename { margin: 2px 0 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.badge {
  border: 1px solid #2c313c;
  border-radius: 999px;
  color: #9aa0ab;
  font-size: 12px;
  font-weight: 650;
  padding: 2px 8px;
}
.badge-client { color: #10261b; background: #3dd68c; border-color: #3dd68c; }
</style>
</head>
<body>
<div class="app">
<header class="appbar"><div class="brand">modupdater</div></header>
<section class="card">
<div class="mod-head">
<div class="mod-main">
<div class="mod-title"><span>{{.Name}}</span>{{if .Added}}<span class="ver">{{.Added}}</span>{{end}}<span class="badge{{if ne .Kind "серверный мод"}} badge-client{{end}}">{{.Kind}}</span><span class="ver">{{.Version}}</span></div>
<p class="filename">{{.Filename}}</p>
</div>
<a class="dl" href="/api/mods/{{.ID}}/download" aria-label="Скачать">{{template "arrow"}}</a>
</div>
<p class="desc">{{.Description}}</p>
</section>
</div>
</body>
</html>
{{end}}
{{define "changelog"}}<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Чейнжлог</title>
<style>
:root { color-scheme: dark; }
* { box-sizing: border-box; }
body {
  margin: 0;
  font-family: "Segoe UI", system-ui, sans-serif;
  line-height: 1.4;
  color: #e7e8ea;
  background: #14161c;
}
.app { min-height: 100vh; padding: 20px 24px 32px; }
.appbar { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
.brand { font-size: 22px; font-weight: 700; letter-spacing: -0.03em; }
.nav { color: #3dd68c; font-weight: 650; text-decoration: none; }
.nav:hover { color: #8ef0c0; }
.card {
  background: #1c1f27;
  border: 1px solid #2c313c;
  border-radius: 10px;
  padding: 16px;
  margin-bottom: 8px;
}
.hint { color: #9aa0ab; margin: 8px 0; }
.day { font-weight: 650; }
.changelog-lines { margin: 8px 0 0; padding: 0; list-style: none; }
.changelog-lines li { padding: 4px 0; }
</style>
</head>
<body>
<div class="app">
<header class="appbar"><div class="brand">modupdater</div><a class="nav" href="/">Моды</a></header>
{{if .Days}}
{{range .Days}}
<article class="card">
<div class="day">{{.Day}}</div>
<ul class="changelog-lines">
{{range .Lines}}<li>{{.}}</li>{{end}}
</ul>
</article>
{{end}}
{{else}}
<p class="hint">Нет изменений.</p>
{{end}}
</div>
</body>
</html>
{{end}}
`))

type downloadMod struct {
	ID             string
	Name           string
	Filename       string
	Version        string
	Client         bool
	ClientOnly     bool
	ClientRequired bool
	Added          string
}

func (m downloadMod) Kind() string {
	k := modKind(m.Client, m.ClientOnly, m.ClientRequired)
	if k == "серверный мод" {
		return ""
	}
	return k
}

type downloadPageData struct {
	HasRelease    bool
	Version       string
	LinuxSize     string
	WindowsSize   string
	LinuxSHA256   string
	WindowsSHA256 string
	Mods          []downloadMod
}

func (a *API) downloadPage(w http.ResponseWriter, r *http.Request, _ map[string]string) {
	list, err := a.mods.ListAll()
	if err != nil {
		writeMapped(w, err)
		return
	}
	rel, ok, err := a.mods.Latest()
	if err != nil {
		writeMapped(w, err)
		return
	}
	data := downloadPageData{Mods: downloadMods(list)}
	if ok {
		data.HasRelease = true
		data.Version = rel.Version
		data.LinuxSize = formatSize(rel.LinuxSize)
		data.WindowsSize = formatSize(rel.WindowsSize)
		data.LinuxSHA256 = rel.LinuxSHA256
		data.WindowsSHA256 = rel.WindowsSHA256
	}
	writePage(w, "download", data)
}

type modPageData struct {
	ID             string
	Name           string
	Description    string
	Filename       string
	Version        string
	Client         bool
	ClientOnly     bool
	ClientRequired bool
	Added          string
}

func (m modPageData) Kind() string {
	return modKind(m.Client, m.ClientOnly, m.ClientRequired)
}

func (a *API) modPage(w http.ResponseWriter, r *http.Request, p map[string]string) {
	list, err := a.mods.ListAll()
	if err != nil {
		writeMapped(w, err)
		return
	}
	id := p["id"]
	for _, m := range list {
		if m.ID != id {
			continue
		}
		if m.Revoked {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		writePage(w, "mod", modPageData{
			ID: m.ID, Name: m.Name, Description: m.Description, Filename: m.Filename,
			Version: m.Version, Client: m.Client, ClientOnly: m.ClientOnly, ClientRequired: m.ClientRequired,
			Added: addedText(m.CreatedAt),
		})
		return
	}
	writeError(w, http.StatusNotFound, "not_found")
}

func modKind(client, clientOnly, clientRequired bool) string {
	if clientOnly && !clientRequired {
		return "необязательный"
	}
	if clientRequired || (client && !clientOnly) {
		return "обязательный"
	}
	return "серверный мод"
}

func writePage(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(http.StatusOK)
	_ = downloadPageTmpl.ExecuteTemplate(w, name, data)
}

func downloadMods(list []store.Mod) []downloadMod {
	rows := make([]store.Mod, 0, len(list))
	for _, m := range list {
		if !m.Revoked {
			rows = append(rows, m)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		in, jn := strings.ToLower(rows[i].Name), strings.ToLower(rows[j].Name)
		if in != jn {
			return in < jn
		}
		af, bf := strings.ToLower(rows[i].Filename), strings.ToLower(rows[j].Filename)
		if af != bf {
			return af < bf
		}
		return rows[i].Filename < rows[j].Filename
	})
	out := make([]downloadMod, 0, len(rows))
	for _, m := range rows {
		out = append(out, downloadMod{
			ID: m.ID, Name: m.Name, Filename: m.Filename, Version: m.Version,
			Client: m.Client, ClientOnly: m.ClientOnly, ClientRequired: m.ClientRequired, Added: addedText(m.CreatedAt),
		})
	}
	return out
}

func addedText(ts string) string {
	if ts == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	return "добавлен " + t.In(time.Local).Format("02.01.2006")
}

func formatSize(n int64) string {
	if n < 0 {
		n = 0
	}
	units := []string{"Б", "КБ", "МБ", "ГБ", "ТБ"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", n, units[0])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
