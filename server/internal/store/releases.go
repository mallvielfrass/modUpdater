package store

import (
	"database/sql"
	"errors"
)

type ClientRelease struct {
	Version       string `json:"version"`
	Status        string `json:"status"`
	LinuxSHA256   string `json:"linux_sha256"`
	LinuxSize     int64  `json:"linux_size"`
	WindowsSHA256 string `json:"windows_sha256"`
	WindowsSize   int64  `json:"windows_size"`
	CreatedAt     string `json:"created_at"`
}

const releaseCols = `version, status, linux_sha256, linux_size, windows_sha256, windows_size, created_at`

func scanRelease(sc interface{ Scan(...any) error }) (ClientRelease, error) {
	var r ClientRelease
	err := sc.Scan(&r.Version, &r.Status, &r.LinuxSHA256, &r.LinuxSize, &r.WindowsSHA256, &r.WindowsSize, &r.CreatedAt)
	return r, err
}

func (db *DB) GetRelease(version string) (ClientRelease, error) {
	row := db.sql.QueryRow(`SELECT `+releaseCols+` FROM client_releases WHERE version=?`, version)
	return scanReleaseRow(row)
}

func scanReleaseRow(row *sql.Row) (ClientRelease, error) {
	r, err := scanRelease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ClientRelease{}, ErrNotFound
	}
	return r, err
}

func (db *DB) ListReleases() ([]ClientRelease, error) {
	rows, err := db.sql.Query(`SELECT ` + releaseCols + ` FROM client_releases ORDER BY created_at DESC, version ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientRelease{}
	for rows.Next() {
		r, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (db *DB) UpsertDevRelease(r ClientRelease) (bool, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRow(`SELECT status FROM client_releases WHERE version=?`, r.Version).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.Exec(
			`INSERT INTO client_releases (version, status, linux_sha256, linux_size, windows_sha256, windows_size, created_at)
			 VALUES (?, 'dev', ?, ?, ?, ?, ?)`,
			r.Version, r.LinuxSHA256, r.LinuxSize, r.WindowsSHA256, r.WindowsSize, r.CreatedAt,
		)
		if err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
	if err != nil {
		return false, err
	}
	if status != "dev" {
		return false, ErrConflict
	}
	_, err = tx.Exec(
		`UPDATE client_releases
		 SET linux_sha256=?, linux_size=?, windows_sha256=?, windows_size=?, created_at=?
		 WHERE version=? AND status='dev'`,
		r.LinuxSHA256, r.LinuxSize, r.WindowsSHA256, r.WindowsSize, r.CreatedAt, r.Version,
	)
	if err != nil {
		return false, err
	}
	return false, tx.Commit()
}

func (db *DB) PublishRelease(version string) (ClientRelease, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return ClientRelease{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRow(`SELECT `+releaseCols+` FROM client_releases WHERE version=?`, version)
	rel, err := scanRelease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ClientRelease{}, ErrNotFound
	}
	if err != nil {
		return ClientRelease{}, err
	}
	if rel.LinuxSize <= 0 || rel.WindowsSize <= 0 || rel.LinuxSHA256 == "" || rel.WindowsSHA256 == "" {
		return ClientRelease{}, ErrConflict
	}
	if _, err := tx.Exec(`UPDATE client_releases SET status='released' WHERE version=?`, version); err != nil {
		return ClientRelease{}, err
	}
	if _, err := tx.Exec(`UPDATE settings SET active_client_version=? WHERE id=1`, version); err != nil {
		return ClientRelease{}, err
	}
	rel.Status = "released"
	return rel, tx.Commit()
}

func (db *DB) DeleteDevRelease(version string) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRow(`SELECT status FROM client_releases WHERE version=?`, version).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "dev" {
		return ErrConflict
	}
	res, err := tx.Exec(`DELETE FROM client_releases WHERE version=? AND status='dev'`, version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}
