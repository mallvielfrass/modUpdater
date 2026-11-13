# 07-client-admin

id: 07-client-admin
depends: [05-client-shell, 06-client-user]
spec: 00-overview.md

goal: Lock icon, admin login, admin list with invites, and the server mod list with upload, replace, delete, restart, and logs.

create:
- client/src/ui/Login.tsx
- client/src/ui/Admin.tsx

steps:
1. Add the lock control from spec section `ui`.
2. Login calls `POST /api/auth/login` and `save_session`. A failed login shows the Russian sentence `Неверное имя или пароль.` for `unauthorized` and `Слишком много попыток.` for `rate_limited`.
3. `Закрыть` hides the panel and keeps the session. `Выйти` calls logout and `save_session("")`.
4. Any API 401 clears the session and opens login.
5. Tab `Админы`: list admins, hide delete for `root=true`, delete other admins, create an invite, show its URL in a selectable field. Deleting the current admin clears the session.
6. Tab `Моды`: list `GET /api/admin/mods`, edit `client`, name, and description, add a jar, replace a jar, delete a mod, and run scan. The file input submits multipart to the admin routes.
7. Put `Перезапуск` and `Логи` above the tabs. Restart sends `{ "message": "" }` unless the admin typed a message. Logs request `lines=200` and show the returned lines.
8. Use the Russian labels from spec section `ui`. Map `mods_dir_unset` to `Сначала укажите каталог модов на сервере.`

done_when:
- `npm test` still passes.
- The panel has exactly two tabs.
- The root row has no delete control.
- Upload uses the admin HTTP routes and does not call `install_mod`.
- A 401 response removes `session.json`.

do_not:
- Do not add a third tab.
- Do not show the RCON password or the systemd unit.
- Do not put the session token in a URL or in `localStorage`.
