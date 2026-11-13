package store

type ChangelogEvent struct {
	ID     string
	Day    string
	SortAt string
	Line   string
}

type ChangelogFile struct {
	Kind string
	Name string
}

func (db *DB) ChangelogSeeded() (bool, error) {
	var n int
	err := db.sql.QueryRow(`SELECT seeded FROM changelog_state WHERE id=1`).Scan(&n)
	return n == 1, err
}

func (db *DB) ListChangelogFiles() ([]ChangelogFile, error) {
	rows, err := db.sql.Query(`SELECT kind, name FROM changelog_files ORDER BY kind, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChangelogFile{}
	for rows.Next() {
		var f ChangelogFile
		if err := rows.Scan(&f.Kind, &f.Name); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (db *DB) ListChangelogEvents() ([]ChangelogEvent, error) {
	rows, err := db.sql.Query(`SELECT id, day, sort_at, line FROM changelog_events ORDER BY day, sort_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChangelogEvent{}
	for rows.Next() {
		var e ChangelogEvent
		if err := rows.Scan(&e.ID, &e.Day, &e.SortAt, &e.Line); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (db *DB) ListChangelogHistory() ([]History, error) {
	rows, err := db.sql.Query(`
		SELECT id, mod_id, slug, name, action, filename, version, sha256, size, client, client_only, COALESCE(archive_name, ''), created_at
		FROM history WHERE action IN ('added', 'deleted')
		ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []History{}
	for rows.Next() {
		h, err := scanHistory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (db *DB) SaveChangelog(events []ChangelogEvent, files []ChangelogFile, replaceFiles bool) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, e := range events {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO changelog_events (id, day, sort_at, line) VALUES (?, ?, ?, ?)`,
			e.ID, e.Day, e.SortAt, e.Line,
		); err != nil {
			return err
		}
	}
	if replaceFiles {
		if _, err := tx.Exec(`DELETE FROM changelog_files`); err != nil {
			return err
		}
		for _, f := range files {
			if _, err := tx.Exec(`INSERT INTO changelog_files (kind, name) VALUES (?, ?)`, f.Kind, f.Name); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE changelog_state SET seeded=1 WHERE id=1`); err != nil {
			return err
		}
	}
	return tx.Commit()
}
