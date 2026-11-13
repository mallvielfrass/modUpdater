ALTER TABLE mods ADD COLUMN created_at TEXT NOT NULL DEFAULT '';
UPDATE mods SET created_at = COALESCE((SELECT MIN(h.created_at) FROM history AS h WHERE h.mod_id = mods.id AND h.action = 'added'), mods.updated_at);
