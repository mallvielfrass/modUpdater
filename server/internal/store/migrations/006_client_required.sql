ALTER TABLE mods ADD COLUMN client_required INTEGER NOT NULL DEFAULT 0 CHECK (client_required IN (0, 1));
