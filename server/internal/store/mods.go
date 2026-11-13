package store

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
)

func (db *DB) ModsDir() (string, error) {
	var dir string
	err := db.sql.QueryRow(`SELECT mods_dir FROM settings WHERE id=1`).Scan(&dir)
	return dir, err
}

func (db *DB) SetModsDir(dir string) error {
	_, err := db.sql.Exec(`UPDATE settings SET mods_dir=? WHERE id=1`, dir)
	return err
}

func (db *DB) LastRestartAt() (string, error) {
	var ts string
	err := db.sql.QueryRow(`SELECT last_restart_at FROM settings WHERE id=1`).Scan(&ts)
	return ts, err
}

func (db *DB) SetLastRestartAt(ts string) error {
	_, err := db.sql.Exec(`UPDATE settings SET last_restart_at=? WHERE id=1`, ts)
	return err
}

func (db *DB) ActiveClientVersion() (string, error) {
	var v string
	err := db.sql.QueryRow(`SELECT active_client_version FROM settings WHERE id=1`).Scan(&v)
	return v, err
}

func scanMod(sc interface{ Scan(...any) error }) (Mod, error) {
	var m Mod
	var client, clientOnly, revoked, clientRequired int
	err := sc.Scan(&m.ID, &m.Slug, &m.Name, &m.Description, &m.Filename, &m.SHA256, &m.Size, &m.Version, &client, &clientOnly, &revoked, &m.UpdatedAt, &m.CreatedAt, &clientRequired)
	m.Client = client == 1
	m.ClientOnly = clientOnly == 1
	m.Revoked = revoked == 1
	m.ClientRequired = clientRequired == 1
	return m, err
}

const modCols = `id, slug, name, description, filename, sha256, size, version, client, client_only, revoked, updated_at, created_at, client_required`

func (db *DB) ListMods(clientOnly bool) ([]Mod, error) {
	q := `SELECT ` + modCols + ` FROM mods`
	if clientOnly {
		q += ` WHERE client=1 AND revoked=0`
	}
	q += ` ORDER BY name COLLATE NOCASE, filename COLLATE NOCASE`
	rows, err := db.sql.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mod{}
	for rows.Next() {
		m, err := scanMod(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		return modLess(out[i], out[j])
	})
	return out, nil
}

func (db *DB) ListRevoked() ([]Mod, error) {
	rows, err := db.sql.Query(`SELECT ` + modCols + ` FROM mods WHERE revoked=1 AND client=1 ORDER BY name COLLATE NOCASE, filename COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mod{}
	for rows.Next() {
		m, err := scanMod(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (db *DB) SetRevoked(id, updated string, h *History) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE mods SET revoked=1, updated_at=? WHERE id=? AND revoked=0`, updated, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if h != nil {
		if err := insertHistory(tx, *h); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) ClearRevoked(id, updated string, h *History) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE mods SET revoked=0, updated_at=? WHERE id=? AND revoked=1`, updated, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if h != nil {
		if err := insertHistory(tx, *h); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) DeleteMod(id string) error {
	res, err := db.sql.Exec(`DELETE FROM mods WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func modLess(a, b Mod) bool {
	an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name)
	if an != bn {
		return an < bn
	}
	af, bf := strings.ToLower(a.Filename), strings.ToLower(b.Filename)
	if af != bf {
		return af < bf
	}
	return a.Filename < b.Filename
}

func (db *DB) GetMod(id string) (Mod, error) {
	row := db.sql.QueryRow(`SELECT `+modCols+` FROM mods WHERE id=?`, id)
	m, err := scanMod(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Mod{}, ErrNotFound
	}
	return m, err
}

func (db *DB) ModBySlug(slug string) (Mod, error) {
	row := db.sql.QueryRow(`SELECT `+modCols+` FROM mods WHERE slug=?`, slug)
	m, err := scanMod(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Mod{}, ErrNotFound
	}
	return m, err
}

func (db *DB) ModByFilename(name string) (Mod, error) {
	row := db.sql.QueryRow(`SELECT `+modCols+` FROM mods WHERE filename=?`, name)
	m, err := scanMod(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Mod{}, ErrNotFound
	}
	return m, err
}

func (db *DB) InsertModWithHistory(m Mod, h History) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`INSERT INTO mods (id, slug, name, description, filename, sha256, size, version, client, client_only, revoked, updated_at, created_at, client_required)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Slug, m.Name, m.Description, m.Filename, m.SHA256, m.Size, m.Version, boolInt(m.Client), boolInt(m.ClientOnly), boolInt(m.Revoked), m.UpdatedAt, m.CreatedAt, boolInt(m.ClientRequired),
	); err != nil {
		return err
	}
	if err := insertHistory(tx, h); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) UpdateModSame(id, filename, version string, size int64, updated string) error {
	_, err := db.sql.Exec(
		`UPDATE mods SET filename=?, version=?, size=?, updated_at=? WHERE id=?`,
		filename, version, size, updated, id,
	)
	return err
}

func (db *DB) UpdateModFile(m Mod, h History) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		`UPDATE mods SET filename=?, sha256=?, size=?, version=?, name=?, description=?, client=?, client_required=?, updated_at=? WHERE id=?`,
		m.Filename, m.SHA256, m.Size, m.Version, m.Name, m.Description, boolInt(m.Client), boolInt(m.ClientRequired), m.UpdatedAt, m.ID,
	); err != nil {
		return err
	}
	if h.ID != "" {
		if err := insertHistory(tx, h); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) UpdateModMeta(id, name, description string, client bool, clientRequired bool, updated string) error {
	_, err := db.sql.Exec(
		`UPDATE mods SET name=?, description=?, client=?, client_required=?, updated_at=? WHERE id=?`,
		name, description, boolInt(client), boolInt(clientRequired), updated, id,
	)
	return err
}

func (db *DB) DeleteModWithHistory(id string, h History) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertHistory(tx, h); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM mods WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func insertHistory(tx *sql.Tx, h History) error {
	var arch any
	if h.ArchiveName != "" {
		arch = h.ArchiveName
	}
	_, err := tx.Exec(
		`INSERT INTO history (id, mod_id, slug, name, action, filename, version, sha256, size, client, client_only, archive_name, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		h.ID, h.ModID, h.Slug, h.Name, h.Action, h.Filename, h.Version, h.SHA256, h.Size, boolInt(h.Client), boolInt(h.ClientOnly), arch, h.CreatedAt,
	)
	return err
}

func (db *DB) GetHistory(id string) (History, error) {
	row := db.sql.QueryRow(`
		SELECT id, mod_id, slug, name, action, filename, version, sha256, size, client, client_only, COALESCE(archive_name, ''), created_at
		FROM history WHERE id=?`, id)
	h, err := scanHistory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return History{}, ErrNotFound
	}
	return h, err
}

func (db *DB) ListPublicHistory(limit int) ([]History, error) {
	rows, err := db.sql.Query(`
		SELECT h.id, h.mod_id, h.slug, h.name, h.action, h.filename, h.version, h.sha256, h.size, h.client,
		       h.client_only, COALESCE(h.archive_name, ''), h.created_at
		FROM history h
		LEFT JOIN mods m ON m.id = h.mod_id
		WHERE h.client = 1 AND (m.id IS NULL OR m.client = 1)
		ORDER BY h.created_at DESC, h.id DESC
		LIMIT ?`, limit)
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

func (db *DB) CountHistory() (int, error) {
	var n int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM history`).Scan(&n)
	return n, err
}

func scanHistory(sc interface{ Scan(...any) error }) (History, error) {
	var h History
	var client, clientOnly int
	err := sc.Scan(&h.ID, &h.ModID, &h.Slug, &h.Name, &h.Action, &h.Filename, &h.Version, &h.SHA256, &h.Size, &client, &clientOnly, &h.ArchiveName, &h.CreatedAt)
	h.Client = client == 1
	h.ClientOnly = clientOnly == 1
	return h, err
}
