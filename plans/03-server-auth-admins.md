# 03-server-auth-admins

id: 03-server-auth-admins
depends: [01-server-core]
spec: 00-overview.md

goal: Admin list, root protection, single-use invites, and the registration page.

create:
- server/internal/httpapi/invite.go
- server/internal/auth/invite_test.go

steps:
1. Implement `GET /api/admin/admins` and `DELETE /api/admin/admins/{id}` from spec section `http`.
2. Delete the root admin only with `forbidden`. Cascade sessions through the foreign key.
3. Implement `POST /api/admin/invites` and `DELETE /api/admin/invites/{token}`.
4. Build the invite URL from the stored origin plus `/invite/` plus the token. Do not string-concatenate a raw `public_url` that could contain a path.
5. Implement register in one `BEGIN IMMEDIATE` transaction from spec section `http`.
6. Accept both JSON and form registration.
7. Serve `GET /invite/{token}` from spec section `http` for every token shape. Use `html/template`.
8. Any authenticated admin may call these routes. A missing session is `unauthorized`.

done_when:
- `go test ./...` in `server/` passes.
- Two concurrent registrations with one invite create one admin.
- An expired invite and a reused invite return `invite_invalid`.
- A duplicate username returns `username_taken`.
- Deleting the root admin returns `forbidden` and leaves the row in place.
- Deleting a non-root admin removes that admin's sessions.
- The invite HTML does not print the submitted username or password back.
- `ttl_hours` of 0 and 337 return `invalid_json`.

do_not:
- Do not add admin roles beyond the root flag.
- Do not add `GET /api/admin/invites`.
- Do not return password hashes.
