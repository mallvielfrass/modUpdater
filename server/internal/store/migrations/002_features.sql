ALTER TABLE settings ADD COLUMN last_restart_at TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN active_client_version TEXT NOT NULL DEFAULT '';
ALTER TABLE mods ADD COLUMN client_only INTEGER NOT NULL DEFAULT 0 CHECK (client_only IN (0, 1));
ALTER TABLE history ADD COLUMN client_only INTEGER NOT NULL DEFAULT 0 CHECK (client_only IN (0, 1));
CREATE TABLE mod_configs (
  id TEXT PRIMARY KEY,
  mod_id TEXT NOT NULL REFERENCES mods(id) ON DELETE CASCADE,
  dest TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('file', 'dir')),
  filename TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  size INTEGER NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE client_releases (
  version TEXT PRIMARY KEY,
  status TEXT NOT NULL CHECK (status IN ('dev', 'released')),
  linux_sha256 TEXT NOT NULL DEFAULT '',
  linux_size INTEGER NOT NULL DEFAULT 0,
  windows_sha256 TEXT NOT NULL DEFAULT '',
  windows_size INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
