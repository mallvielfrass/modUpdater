# 05-client-shell

id: 05-client-shell
depends: []
spec: 00-overview.md

goal: Tauri shell that stores config and session beside the executable and can scan, bak, and download jars without leaving the configured mods directory.

create:
- client/package.json
- client/index.html
- client/vite.config.ts
- client/tsconfig.json
- client/src/main.tsx
- client/src/App.tsx
- client/src/styles.css
- client/src-tauri/Cargo.toml
- client/src-tauri/tauri.conf.json
- client/src-tauri/capabilities/default.json
- client/src-tauri/src/lib.rs
- client/src-tauri/src/jars.rs

steps:
1. Create the Tauri 2 and Vite React TypeScript project by writing the files. Do not run an interactive scaffolder.
2. Rust dependencies: `tauri` 2, `tauri-plugin-dialog`, `serde`, `serde_json`, `sha2`, `zip`, and `reqwest` with `default-features = false` and features `blocking` and `rustls-tls`.
3. Enable the dialog plugin only. Do not enable the shell plugin or the FS plugin.
4. Implement the config directory rule, `config.json`, and `session.json` from spec section `client`.
5. Implement `SafeJarName` with the same steps as spec section `paths`.
6. Implement `load_config`, `save_config`, `load_session`, `save_session`, `scan_mods`, `install_mod`, and `backup_mod` from spec section `client`.
7. Build the download URL with `url.URL` from the configured scheme and host. Refuse any other host and follow no redirect.
8. Leave `App.tsx` as a placeholder that renders the later screens. This task does not build the user or admin forms.

done_when:
- `cargo test` in `client/src-tauri` passes.
- Tests place the config next to a normal executable and next to `App.app/Contents/MacOS/<bin>`.
- Tests reject `server_url` values that contain a path, a user, a query, or a non-http scheme.
- `scan_mods` skips symlinks, directories, and names that are not a safe `.jar`.
- `install_mod` deletes its temp file and leaves the existing jar when the hash differs.
- `install_mod` refuses `mod_id` `../abc` and a filename with a slash.
- A bak of `sodium-0.6.jar` produces `sodium-0.6.jar.<unix>.bak` inside the mods directory.
- The capabilities file does not grant shell or unrestricted filesystem access.

do_not:
- Do not accept a directory argument or a raw download URL in the commands.
- Do not write config into the OS app-data directory.
- Do not extract jars to disk while reading metadata.
