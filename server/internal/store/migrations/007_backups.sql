CREATE TABLE backups (
  id TEXT PRIMARY KEY,
  created_at TEXT NOT NULL,
  label TEXT NOT NULL,
  current INTEGER NOT NULL DEFAULT 0 CHECK (current IN (0, 1))
);
CREATE UNIQUE INDEX backups_one_current ON backups (current) WHERE current = 1;
