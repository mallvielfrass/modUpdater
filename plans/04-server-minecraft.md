# 04-server-minecraft

id: 04-server-minecraft
depends: [01-server-core]
spec: 00-overview.md

goal: Warn the Minecraft server through one RCON `say`, then restart its systemd unit, and return a bounded journal slice.

create:
- server/internal/minecraft/rcon.go
- server/internal/minecraft/systemd.go
- server/internal/minecraft/command_test.go

steps:
1. Implement `SayCommand` from spec section `minecraft`.
2. Implement a small RCON client: one TCP connection, one auth packet, one command packet, 5s dial timeout. Treat auth id `-1` as failure.
3. Implement `POST /api/admin/server/restart` from spec section `http` and spec section `minecraft`.
4. Use a restart mutex that is not the mods mutex.
5. Run `systemctl` and `journalctl` only with the argument arrays in spec section `minecraft`.
6. Read `systemd_unit` and RCON settings from the loaded config. Ignore any unit name or RCON field in the JSON body.
7. Implement `GET /api/admin/server/logs`.

done_when:
- `go test ./...` in `server/` passes.
- `SayCommand` tests show a newline, CR, and NUL do not create a second command, and the result is one `say` string of at most 200 runes plus the `say ` prefix.
- An empty restart message uses `restart_warning`.
- Argument tests show `systemctl restart <unit>` and the `journalctl` args from the spec, with no shell metacharacters interpreted.
- A unit that fails `^[A-Za-z0-9:._@-]+$` is rejected at config load.
- The handler tests use a fake say and a fake command runner. They do not call the real `systemctl`.
- A failed say returns `rcon_failed` and does not call the restart runner.
- A second overlapping restart returns `restart_in_progress`.

do_not:
- Do not take the unit name, RCON host, RCON port, or RCON password from the request.
- Do not run commands through `sh -c`.
- Do not log the RCON password.
