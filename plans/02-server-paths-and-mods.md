# 02-server-paths-and-mods

id: 02-server-paths-and-mods
depends: [01-server-core]
spec: 00-overview.md

goal: Mod directory, scan, archive, history, and the public and admin mod routes, with path checks that refuse files outside the mods dir and the archive.

create:
- server/internal/mods/paths.go
- server/internal/mods/paths_test.go
- server/internal/mods/jar.go
- server/internal/mods/mods.go
- server/internal/mods/scan_test.go
- server/internal/store/mods.go
- server/internal/httpapi/public.go
- server/internal/httpapi/public_test.go
- server/internal/httpapi/admin.go

steps:
1. Implement `SafeJarName`, `SafeArchiveName`, `ResolveModsDir`, and `OpenRegular` from spec section `paths`.
2. Implement jar metadata reads from spec section `paths`. Do not extract zip entries.
3. Implement scan, add, update, and delete from spec section `mods`, including the mutex.
4. Re-validate `filename` and `archive_name` loaded from SQLite before opening them.
5. Serve the public mod and history routes from spec section `http`, including the visibility rule in spec section `mods`.
6. Serve `GET /api/admin/mods`, `POST /api/admin/mods`, `PUT /api/admin/mods/{id}`, `DELETE /api/admin/mods/{id}`, `GET /api/admin/settings`, `PUT /api/admin/settings`, and `POST /api/admin/mods/scan`.
7. Cap JSON bodies at 1 MiB and mod multipart bodies at 256 MiB.
8. Set `Content-Disposition` from the stored safe filename only.

done_when:
- `go test ./...` in `server/` passes.
- Path tests reject `../x.jar`, an absolute name, a `..` segment, a symlink, mods dir `/`, and a mods dir equal to or inside `data_dir`.
- `OpenRegular` rejects archive name `../../x`.
- Scan tests keep `client`, `name`, and `description` when the jar hash changes.
- Scan tests put two jars with one slug into `conflicts` and leave the existing row unchanged.
- Public HTTP tests return the same `not_found` body for an unknown id and for a live `client=false` mod file.
- A history file whose live mod is now `client=false` returns `not_found`.
- A deleted client mod with `history.client=1` and a real archive file is still downloadable.
- Add of a slug that already exists returns `slug_exists`.
- Add refuses to replace an untracked jar and returns `conflict`.

do_not:
- Do not register `http.FileServer`.
- Do not accept a client-supplied archive path or mods path on the download routes.
- Do not write history for a metadata-only update.
