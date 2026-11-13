# modupdater spec

normative: true
ui_language: ru
toolchain: go1.27, rustc 1.99, node 20
products:
  server: server/
  client: client/
task_order:
  - 01-server-core.md
  - 02-server-paths-and-mods.md
  - 03-server-auth-admins.md
  - 04-server-minecraft.md
  - 05-client-shell.md
  - 06-client-user.md
  - 07-client-admin.md

If a task file and this file disagree, this file wins.

## layout

server module path: `modupdater`

- `server/cmd/modupdater/main.go`
- `server/config.example.json`
- `server/internal/config/config.go`
- `server/internal/store/db.go`
- `server/internal/store/auth.go`
- `server/internal/store/mods.go`
- `server/internal/store/migrations/001_init.sql`
- `server/internal/auth/auth.go`
- `server/internal/mods/paths.go`
- `server/internal/mods/jar.go`
- `server/internal/mods/mods.go`
- `server/internal/minecraft/rcon.go`
- `server/internal/minecraft/systemd.go`
- `server/internal/httpapi/httpapi.go`
- `server/internal/httpapi/public.go`
- `server/internal/httpapi/auth.go`
- `server/internal/httpapi/admin.go`
- `server/internal/httpapi/invite.go`

Handlers call auth, mods, and minecraft functions. Those functions call `store`. No interface with one implementation. No ORM. No `http.FileServer`. SQL uses `?` placeholders only.

client:

- `client/src/api.ts`
- `client/src/types.ts`
- `client/src/sync.ts`
- `client/src/ui/Settings.tsx`
- `client/src/ui/UserMods.tsx`
- `client/src/ui/Login.tsx`
- `client/src/ui/Admin.tsx`
- `client/src/App.tsx`
- `client/src/styles.css`
- `client/src-tauri/src/lib.rs`
- `client/src-tauri/src/jars.rs`

No UI kit. No client README. Rust does disk IO. The webview does not get a broad filesystem capability.

## config

Search order: flag `-config`, else `<directory of the server executable>/config.json`.

Relative `data_dir` is joined to the config file directory, then `Abs`. It is not resolved from the process cwd.

```json
{
  "listen": ":8080",
  "data_dir": "./data",
  "public_url": "http://127.0.0.1:8080",
  "root_admin": { "username": "admin", "password": "" },
  "minecraft": {
    "systemd_unit": "minecraft.service",
    "restart_warning": "Перезапуск сервера через 15 секунд: обновление модов",
    "restart_delay_sec": 15,
    "rcon": { "host": "127.0.0.1", "port": 25575, "password": "" }
  }
}
```

Load rejects the process with a non-zero exit when:

- `listen` is empty
- `data_dir` is empty
- `public_url` is not `http` or `https`, has no host, has a user, a query, a fragment, or a path other than empty or `/`
- `systemd_unit` does not match `^[A-Za-z0-9:._@-]+$`
- `rcon.port` is outside 1..65535
- `restart_delay_sec` is outside 0..120
- `root_admin.username` is set and fails the username rule

Store `public_url` with no trailing slash. Empty root username becomes `admin`.

On startup, create `data_dir`, `data_dir/archive`, and the SQLite file `data_dir/modupdater.db`. If no `root=1` admin exists, insert one. Empty password: 20 characters from `crypto/rand` over `ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789`, print `root_password=<value>` once. A later start does not reset that password. Do not write the password back into the config file. Do not log any other password, session token, or invite token.

`http.Server`: `ReadHeaderTimeout` 5s, `IdleTimeout` 60s, `MaxHeaderBytes` 1 MiB. Leave `ReadTimeout` and `WriteTimeout` unset. Upload and the restart delay exceed a short write timeout.

## schema

SQLite through `modernc.org/sqlite`, driver name `sqlite`. After open: `PRAGMA foreign_keys=ON`, `PRAGMA journal_mode=WAL`, `PRAGMA busy_timeout=5000`.

Timestamps are UTC `time.RFC3339` (`2006-01-02T15:04:05Z`).

```sql
CREATE TABLE admins (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  root INTEGER NOT NULL CHECK (root IN (0, 1)),
  created_at TEXT NOT NULL
);
CREATE TABLE sessions (
  token TEXT PRIMARY KEY,
  admin_id TEXT NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL
);
CREATE TABLE invites (
  token TEXT PRIMARY KEY,
  created_by TEXT REFERENCES admins(id) ON DELETE SET NULL,
  expires_at TEXT NOT NULL,
  used_by TEXT REFERENCES admins(id) ON DELETE SET NULL,
  used_at TEXT
);
CREATE TABLE settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  mods_dir TEXT NOT NULL DEFAULT ''
);
CREATE TABLE mods (
  id TEXT PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  filename TEXT NOT NULL UNIQUE,
  sha256 TEXT NOT NULL,
  size INTEGER NOT NULL,
  version TEXT NOT NULL DEFAULT '',
  client INTEGER NOT NULL CHECK (client IN (0, 1)),
  updated_at TEXT NOT NULL
);
CREATE TABLE history (
  id TEXT PRIMARY KEY,
  mod_id TEXT NOT NULL,
  slug TEXT NOT NULL,
  name TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('added', 'updated', 'deleted')),
  filename TEXT NOT NULL,
  version TEXT NOT NULL DEFAULT '',
  sha256 TEXT NOT NULL,
  size INTEGER NOT NULL,
  client INTEGER NOT NULL CHECK (client IN (0, 1)),
  archive_name TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX history_created ON history (created_at DESC);
```

Migration inserts `settings(id=1, mods_dir='')`.

`has_file` is not a column. It is true only when `archive_name` matches `^[0-9a-f]{32}\.jar$` and that regular file is inside `data_dir/archive`.

## ids

- row id: 16 `crypto/rand` bytes, lowercase hex, `^[0-9a-f]{32}$`
- session token and invite token: 32 `crypto/rand` bytes, lowercase hex, `^[0-9a-f]{64}$`
- session lifetime: 30 days from issue
- invite default lifetime: 72 hours
- bcrypt cost: 12

A path segment that is not a valid id is `not_found`. A token is never joined onto a filesystem path.

## paths

`SafeJarName(raw)`:

1. Reject the raw string when it contains NUL, CR, LF, or `"`.
2. Split on `/` and `\`.
3. Reject when any segment is `..`.
4. Keep the last segment.
5. Accept it only when length is 5..180, it matches `^[A-Za-z0-9(\[][A-Za-z0-9._+()\[\] -]*\.jar$`, and it does not contain `..`.

`SafeArchiveName` accepts only `^[0-9a-f]{32}\.jar$`.

`ResolveModsDir(dataDir, raw)`:

1. `Abs`, then `EvalSymlinks`.
2. The result must be an existing directory.
3. Reject `/`.
4. Resolve `dataDir` the same way after creating it.
5. Reject when the two paths are equal or either contains the other.

`OpenRegular(root, name)`:

1. `name` is a single validated segment.
2. `Lstat(Join(root, name))` must be a regular file. Reject a symlink.
3. `filepath.Rel(root, path)` must equal `name`.
4. Open that file and `Stat` the descriptor. Reject when it is not a regular file.
5. Before open, run `SafeJarName` or `SafeArchiveName` again on a name loaded from SQLite.

Before every mod mutation or scan, re-run `ResolveModsDir` on the stored path. A symlink that later points at `data_dir` or `/` fails the request with `invalid_mods_dir`.

Directory reads use `ReadDir` on that one directory. Do not walk. Skip directories, symlinks, and names that fail `SafeJarName`.

Jar bytes are written with `CreateTemp` in the destination directory. The temp name must not end in `.jar`. Hash while copying. Cap the copy at the route limit plus one byte and return `payload_too_large` when the cap is hit. Rename into the final name on the same directory. Cross-device archive move: copy the regular file, then remove the source. A failed write removes the temp file.

Metadata reads do not extract the zip. Open only entries whose name is exactly `fabric.mod.json`, `quilt.mod.json`, or `META-INF/mods.toml`. Read at most 1 MiB from an entry. A parse failure uses the filename stem as slug and an empty version.

Field limits:

- username: 3..32, `^[A-Za-z0-9_-]+$`
- password: 8..128
- slug: 1..128 bytes, no `/`, `\`, NUL, CR, LF
- name: 1..200
- description: 0..4000
- version: 0..64
- admin-supplied name or description over the limit: `invalid_json`
- metadata over the limit: truncate name, description, and version; skip the file when the slug is still invalid

Slug source: trimmed metadata id, else the jar stem. Fabric `description` may be a string or an array of strings. From the first `mods.toml` `[[mods]]` table, read `modId`, `version`, `displayName`, and `description` until the next `[[`.

## mods

One mutex covers set-dir, scan, add, update, and delete. The restart mutex is separate.

Live jars stay in `mods_dir`. Replaced and deleted bytes go to `data_dir/archive/<history id>.jar`.

History rows are written only for `added`, `updated`, and `deleted` file events. Editing name, description, or `client` does not write history.

Scan result shape:

```json
{ "mods_dir": "", "added": 0, "updated": 0, "removed": 0, "conflicts": [], "skipped": [] }
```

Scan algorithm:

1. Collect top-level regular `.jar` files that pass `SafeJarName`.
2. Parse slug, version, and hashes. Zero-byte, unreadable, and invalid-slug files go to `skipped`.
3. A slug that maps to two or more remaining files: add those filenames to `conflicts` and do not change that slug's row.
4. A file whose filename is already owned by a row with a different slug: add it to `conflicts` and skip it.
5. Same slug and same sha256: update `filename`, `version`, and `size` only. Do not write history.
6. Same slug and different sha256: update `filename`, `sha256`, `size`, and `version`. Write `updated`. Move the old tracked file into the archive only when it still exists, is a different path, and its hash still matches the stored hash. Otherwise leave `archive_name` empty.
7. Do not change `client`, `name`, or `description` on an existing row.
8. Unmatched file: insert `client=false`, name and description from metadata, fallback name = slug. Write `added`.
9. A row whose slug was not seen, whose filename is not a conflict, and whose file is absent: delete the row and write `deleted` with empty `archive_name`.

Add:

- Require `mods_dir`. Otherwise `mods_dir_unset`.
- Multipart field `file` is required. Its filename passes `SafeJarName`.
- Absent `name` or `description` comes from metadata, then slug.
- Absent `client` is false. Present `client` must be `true` or `false`.
- Existing slug returns `slug_exists`.
- Filename owned by another row, or an untracked file already at that path, returns `conflict`. Do not delete an untracked jar.
- Write `added`.

Update:

- Absent multipart keys `name`, `description`, and `client` stay unchanged.
- A new file uses `SafeJarName` on its uploaded filename.
- Filename owned by another row, or an untracked file at the new path, returns `conflict`.
- Archive the current tracked file when it is still a regular file, then write the new bytes and write `updated`.
- A metadata-only update writes no history.

Delete:

- Archive the tracked file when it is still a regular file.
- A missing file still deletes the row, writes `deleted`, and returns 204.

Public visibility:

- Current file endpoints require a live row with `client=1`.
- History list and history file use: `history.client=1` and (`mods` row missing or `mods.client=1`).
- `client=0` and an unknown id return the same `not_found` on public mod routes.
- HTTP error bodies contain no filesystem path and no SQL text.

## http

JSON object on success. Error body is exactly `{ "error": "<code>" }`.

Closed error codes: `invalid_json`, `invalid_name`, `invalid_password`, `invalid_filename`, `invalid_mods_dir`, `invalid_config`, `invalid_limit`, `unauthorized`, `forbidden`, `not_found`, `conflict`, `invite_invalid`, `username_taken`, `mods_dir_unset`, `slug_exists`, `restart_in_progress`, `rcon_failed`, `restart_failed`, `logs_failed`, `rate_limited`, `payload_too_large`, `internal`.

CORS on every response: `Access-Control-Allow-Origin: *`, methods `GET, POST, PUT, DELETE, OPTIONS`, headers `Authorization, Content-Type`. No `Access-Control-Allow-Credentials`. `OPTIONS` returns 204.

JSON bodies except mod uploads: 1 MiB. Mod multipart: 256 MiB.

Session header: `Authorization: Bearer <64 hex>`. Missing, unknown, or expired session is `unauthorized`. An expired row is deleted.

Rate limit login and register by the TCP peer host from `RemoteAddr`. Ignore `X-Forwarded-For`. 10 failures per 10 minutes, then `rate_limited`. A success clears that peer. Unknown username compares bcrypt against a startup dummy hash of cost 12. Login failure for unknown user and bad password is both `unauthorized`.

Routes:

- `GET /api/mods` -> `{ "mods": [Mod] }` for live `client=1`, ordered by name.
- `GET /api/mods/{id}` -> `{ "mod": Mod }` or `not_found`.
- `GET /api/mods/{id}/file` -> current jar. `Content-Type: application/octet-stream`. `Content-Disposition: attachment; filename="<SafeJarName>"`.
- `GET /api/history?limit=50` -> `{ "events": [Event] }`, newest first. Absent limit means 50. Present limit outside 1..200 is `invalid_limit`.
- `GET /api/history/{id}/file` -> archive jar under the public history rule, else `not_found`.
- `POST /api/auth/login` `{ "username", "password" }` -> `{ "token", "expires_at", "admin": Admin }`.
- `POST /api/auth/logout` -> 204 even when the token is absent or unknown. A known token is deleted.
- `GET /api/auth/me` -> `{ "admin": Admin }`. Requires a session.
- `POST /api/auth/register` accepts JSON or `application/x-www-form-urlencoded` with `invite_token`, `username`, `password`.
- `GET /invite/{token}` always returns the registration form. Do not reveal whether the token exists.
- `GET /api/admin/admins` -> `{ "admins": [Admin] }`.
- `DELETE /api/admin/admins/{id}` -> 204. Root target is `forbidden`.
- `POST /api/admin/invites` `{ "ttl_hours": 72 }` -> `{ "invite": { "token", "url", "expires_at" } }`. `ttl_hours` range 1..336. URL is `public_url + "/invite/" + token`.
- `DELETE /api/admin/invites/{token}` -> 204 while unused. Used or unknown is `not_found`.
- `GET /api/admin/settings` -> `{ "mods_dir" }`.
- `PUT /api/admin/settings` `{ "mods_dir" }` -> scan result. Empty string stores an empty dir and does not scan.
- `POST /api/admin/mods/scan` -> scan result.
- `GET /api/admin/mods` -> `{ "mods": [Mod] }` for every live mod.
- `POST /api/admin/mods` -> 201 `{ "mod": Mod }`.
- `PUT /api/admin/mods/{id}` -> `{ "mod": Mod }`.
- `DELETE /api/admin/mods/{id}` -> 204.
- `POST /api/admin/server/restart` `{ "message": "" }` -> `{ "warned": true, "restarted": true }`.
- `GET /api/admin/server/logs?lines=200` -> `{ "unit", "lines": [] }`. Absent lines means 200. Present lines outside 1..5000 is `invalid_limit`.

Mod:

```json
{ "id": "", "slug": "", "name": "", "description": "", "filename": "", "version": "", "sha256": "", "size": 0, "client": true, "updated_at": "" }
```

Event:

```json
{ "id": "", "mod_id": "", "slug": "", "name": "", "action": "updated", "filename": "", "version": "", "sha256": "", "size": 0, "client": true, "created_at": "", "has_file": true }
```

Admin:

```json
{ "id": "", "username": "", "root": true, "created_at": "" }
```

`sha256` is lowercase hex. `client` is JSON bool. SQLite stores it as 0 or 1.

Register consumes the invite in one `BEGIN IMMEDIATE` transaction: update `used_by` only where `used_at` is null and `expires_at` is in the future; require one row; insert the admin with `root=0`; insert a session; commit. Zero updated rows is `invite_invalid`. Duplicate username is `username_taken`.

JSON register returns the login body. Form register returns HTML 200 with the text `Аккаунт создан. Войдите в приложении.` Failure HTML is `Ссылка недействительна или имя занято.` and does not echo the submitted fields.

Invite page is `html/template` with `Referrer-Policy: no-referrer`. The form posts to `/api/auth/register` and carries the token in a hidden field. No third-party script.

Every admin may create invites and delete any non-root admin, including itself. Deleting an admin deletes that admin's sessions through the foreign key. Password hashes are never returned. RCON password and the systemd unit are never returned.

## minecraft

RCON and the unit name come from the config file, never from JSON.

`SayCommand(message)`:

1. Empty message uses `restart_warning`.
2. Remove CR, LF, and NUL.
3. Cut to 200 runes.
4. Return one string: `say ` plus that text.

The RCON client sends one command packet. Auth failure or a dial failure returns `rcon_failed` and does not call systemctl. Dial timeout is 5 seconds.

Restart holds its own mutex. A busy restart is `restart_in_progress`. After a successful say, sleep `restart_delay_sec`, then run `systemctl` with args `restart`, `<unit>`. Non-zero exit is `restart_failed`.

Logs run `journalctl` with args `-u`, `<unit>`, `-n`, `<lines>`, `-o`, `short-iso`, `--no-pager`. Failure is `logs_failed`.

Both commands use argument arrays. Do not start a shell.

## client

Config directory:

1. Canonicalize `current_exe()`.
2. When a path segment ends in `.app` and is later followed by `Contents` then `MacOS`, use the parent of that `.app` directory.
3. Otherwise use the executable parent.
4. Read and write only `config.json` and `session.json` there, mode `0600`.
5. A directory that is not writable returns an error. Do not fall back to the OS app-data directory.

`config.json`: `{ "server_url": "", "mods_dir": "" }`.

`server_url` is empty or an origin: `http` or `https`, host required, no user, path, query, or fragment. `mods_dir` is empty or an existing directory.

`session.json`: `{ "token": "" }`. An empty token deletes the file.

Commands, all directory and URL values taken from those files:

- `load_config() -> Config`
- `save_config(server_url, mods_dir)`
- `load_session() -> String`
- `save_session(token)`
- `scan_mods() -> LocalJar[]`
- `install_mod(mod_id, filename, sha256, backup_filename)`
- `backup_mod(filename)`

`LocalJar`: `filename`, `sha256`, `size`, `slug`, `version`.

`scan_mods` canonicalizes `mods_dir` and lists one level. Same skip rules as the server. Metadata rules match the server. Slug fallback is the jar stem.

`install_mod`:

1. `mod_id` matches `^[0-9a-f]{32}$`.
2. `filename` and a present `backup_filename` pass `SafeJarName`.
3. Build `url.URL` with the configured scheme and host and path `/api/mods/<id>/file`.
4. Download with redirects disabled and a 120s timeout into a temp file in `mods_dir` whose name does not end in `.jar`.
5. Compare sha256 with a constant-time compare. On mismatch, delete the temp file and return `hash_mismatch`.
6. When `backup_filename` is set and differs from `filename`, rename that regular file to bak.
7. When the target filename exists, rename it to bak.
8. Rename the temp file to `filename`.
9. A bak failure deletes the temp file and leaves the remaining jar in place.

Bak name: `<stem>.jar.<unix>.bak`. Stem is the filename without the trailing `.jar`. When that name exists, use `<stem>.jar.<unix>-2.bak` and increment. The bak name is one path segment, stays inside `mods_dir`, and the source must be a regular file.

`backup_mod` performs only step 6's rename for that filename.

Directory choice uses the Tauri dialog plugin. No shell plugin. No FS plugin.

## sync

`syncPlan(remote, local)` in `client/src/sync.ts` is pure.

Sort the local files by filename. Each local file can be used once. For each remote mod, in input order:

1. Unused local file with the same sha256: action `current`. Several matches: lexicographically first filename.
2. Else unused local file with the same non-empty slug: action `replace`. Prefer the local file whose filename equals the remote filename, else the lexicographically first.
3. Else unused local file with the same filename: action `replace`.
4. Else action `install`.

Unused local files become `extra`. They are not deleted automatically.

`current` stores the matched filename. `replace` stores it as `localFilename`. `install` has no `localFilename`.

Update-all runs `replace` and `install` items in order. Pass `backup_filename` only when `localFilename` is set and differs from the remote filename. The first failure stops the sequence and names that mod.

## ui

Russian strings.

- Settings: `URL сервиса`, `Папка модов`, `Сохранить`, `Проверить`.
- User list statuses: `установлен`, `заменить`, `доустановить`.
- Buttons: `Обновить все`, `Обновить`, `Удалить`.
- Extra local jars have no description and only `Удалить`.
- Lock button opens login when no session exists. A valid session opens the admin panel.
- Login: `Имя`, `Пароль`, `Войти`.
- Admin: `Закрыть` returns to the user view and keeps the session. `Выйти` clears it.
- Tabs: `Админы`, `Моды`. No third tab.
- Above the tabs: `Перезапуск`, `Логи`.
- Root admin has no delete button.
- Invite creation shows the URL in a selectable field.
- A 401 from the API clears `session.json` and shows login.

Admin jar upload uses the webview file input and multipart HTTP. It does not write the local mods directory. User install and local delete go through the Rust commands.

## acceptance

- `go test ./...` passes in `server/`.
- Client `npm test` passes the sync tests.
- Public `GET /api/mods/{id}/file` for a `client=false` mod is `not_found`.
- `../`, a symlink jar, `/`, and a mods dir inside `data_dir` do not open or scan as live mods.
- Root admin delete is `forbidden`.
- A used invite cannot create a second admin.
- Restart sends one `say` command before `systemctl`, and a failed say does not restart.
