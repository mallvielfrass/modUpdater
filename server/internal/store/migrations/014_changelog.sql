CREATE TABLE changelog_events (
  id TEXT PRIMARY KEY,
  day TEXT NOT NULL,
  sort_at TEXT NOT NULL,
  line TEXT NOT NULL
);
CREATE INDEX changelog_events_day ON changelog_events (day, sort_at, id);
CREATE TABLE changelog_files (
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  PRIMARY KEY (kind, name)
);
CREATE TABLE changelog_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  seeded INTEGER NOT NULL DEFAULT 0
);
INSERT INTO changelog_state (id, seeded) VALUES (1, 0);
