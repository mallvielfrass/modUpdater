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
INSERT INTO settings (id, mods_dir) VALUES (1, '');
