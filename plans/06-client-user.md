# 06-client-user

id: 06-client-user
depends: [05-client-shell]
spec: 00-overview.md

goal: Settings, comparison of the public mod list with the local folder, update-all, and local bak for one client mod.

create:
- client/src/api.ts
- client/src/types.ts
- client/src/sync.ts
- client/src/sync.test.ts
- client/src/ui/Settings.tsx
- client/src/ui/UserMods.tsx

steps:
1. Add the Mod, Event, and API error types from spec section `http`. `api.ts` reads `{ "error": "<code>" }` and throws that code.
2. Implement `syncPlan` from spec section `sync`.
3. Add vitest and the cases below.
4. Build Settings and UserMods with the Russian strings in spec section `ui`.
5. `Сохранить` calls `save_config`. The directory control uses the dialog plugin.
6. `Проверить` calls `GET /api/mods` and `scan_mods`, then `syncPlan`. Block it when either config field is empty.
7. Show remote mods with status `установлен`, `заменить`, or `доустановить`, plus description and version.
8. `Обновить все` runs the update sequence from spec section `sync`. Show the current index and stop on the first error, including `hash_mismatch`.
9. `Обновить` on one row uses the same install command.
10. `Удалить` calls `backup_mod` for the matched local file, or for an extra local jar. Do not call the server delete route.
11. Show extra local jars without a description and with `Удалить` only.
12. Mount these screens from `App.tsx` as the default view.

done_when:
- `npm test` passes.
- Sync tests cover: hash match wins over a different filename; slug match prefers the equal filename; two slug matches choose the lexicographic filename; filename match is used when the slug differs; an unused local jar is `extra`; each local jar is used once.
- Update-all passes `backup_filename` only when it differs from the remote filename.
- The user screen has no server-admin controls.

do_not:
- Do not delete a local jar without leaving a `.bak`.
- Do not download a mod with `client=false`. The public list is the only remote input.
- Do not add a history screen.
