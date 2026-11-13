# 01-server-core

id: 01-server-core
depends: []
spec: 00-overview.md

goal: Bootable Go server with config, SQLite, root admin, sessions, login, and the error helper.

create:
- server/go.mod
- server/cmd/modupdater/main.go
- server/config.example.json
- server/internal/config/config.go
- server/internal/config/config_test.go
- server/internal/store/db.go
- server/internal/store/auth.go
- server/internal/store/migrations/001_init.sql
- server/internal/auth/auth.go
- server/internal/auth/auth_test.go
- server/internal/httpapi/httpapi.go
- server/internal/httpapi/auth.go

steps:
1. Module path `modupdater`. Dependencies: `modernc.org/sqlite`, `golang.org/x/crypto/bcrypt`.
2. Implement config load and validation from spec section `config`. Copy the example JSON from that section into `server/config.example.json` with an empty root password.
3. Open SQLite from spec section `schema`. Embed `001_init.sql` from package `store` and apply it once.
4. Bootstrap the root admin from spec section `config`.
5. Implement id and token generation from spec section `ids`.
6. Implement login, logout, and `GET /api/auth/me` from spec section `http`.
7. Add `writeError(w, status, code)`. Accept only codes listed in spec section `http`.
8. Add CORS from spec section `http`.
9. Add the login rate limit and the dummy bcrypt compare from spec section `http`.
10. Start `http.Server` with the timeouts from spec section `config`.
11. Register only the routes this task implements. Unknown paths return `not_found`.

done_when:
- `go test ./...` in `server/` passes.
- Config tests reject a `public_url` with a path, a userinfo part, a bad unit name, and `restart_delay_sec` outside 0..120.
- Config tests resolve a relative `data_dir` from the config file directory.
- Auth tests show a second start does not change the root password hash.
- Login tests show the same `unauthorized` body for an unknown user and a bad password.
- The eleventh failed login from one peer inside 10 minutes returns `rate_limited`.

do_not:
- Do not add mod, invite, restart, or log routes in this task.
- Do not log passwords or session tokens.
- Do not add a `/health` route.
