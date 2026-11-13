package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

type Backup struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	Label     string `json:"label"`
	Current   bool   `json:"current"`
	Kind      string `json:"kind,omitempty"`
	Scope     string `json:"scope,omitempty"`
	ParentID  string `json:"parent_id,omitempty"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Storage   string `json:"storage"`
	Dest      string `json:"dest,omitempty"`
	Status    string `json:"status,omitempty"`
	Error     string `json:"error,omitempty"`
}

const backupCols = `id, created_at, label, current, kind, scope, parent_id, size, sha256, storage, status, error, dest`

func (db *DB) DailyBackup() (string, string, error) {
	var hhmm, day string
	err := db.sql.QueryRow(`SELECT daily_backup, daily_backup_day FROM settings WHERE id=1`).Scan(&hhmm, &day)
	return hhmm, day, err
}

func (db *DB) SetDailyBackup(hhmm string) error {
	_, err := db.sql.Exec(`UPDATE settings SET daily_backup=? WHERE id=1`, hhmm)
	return err
}

func (db *DB) SetDailyBackupDay(day string) error {
	_, err := db.sql.Exec(`UPDATE settings SET daily_backup_day=? WHERE id=1`, day)
	return err
}

func (db *DB) BackupScope() (string, error) {
	var scope string
	err := db.sql.QueryRow(`SELECT backup_scope FROM settings WHERE id=1`).Scan(&scope)
	return scope, err
}

func (db *DB) SetBackupScope(scope string) error {
	_, err := db.sql.Exec(`UPDATE settings SET backup_scope=? WHERE id=1`, scope)
	return err
}

func (db *DB) AddBackup(b Backup) error {
	if !ValidID(b.ID) || b.CreatedAt == "" || b.Label == "" {
		return ErrConflict
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE backups SET current=0`); err != nil {
		tx.Rollback()
		return err
	}
	storage := b.Storage
	if storage == "" {
		storage = "disk"
	}
	dest := b.Dest
	if dest == "" {
		dest = "disk"
	}
	if _, err := tx.Exec(
		`INSERT INTO backups (id, created_at, label, current, kind, scope, parent_id, size, sha256, storage, status, error, dest) VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.CreatedAt, b.Label, b.Kind, b.Scope, b.ParentID, b.Size, b.SHA256, storage, b.Status, b.Error, dest,
	); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (db *DB) AddBackupInactive(b Backup) error {
	if !ValidID(b.ID) || b.CreatedAt == "" || b.Label == "" {
		return ErrConflict
	}
	storage := b.Storage
	if storage == "" {
		storage = "disk"
	}
	dest := b.Dest
	if dest == "" {
		dest = "disk"
	}
	_, err := db.sql.Exec(
		`INSERT INTO backups (id, created_at, label, current, kind, scope, parent_id, size, sha256, storage, status, error, dest) VALUES (?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.ID, b.CreatedAt, b.Label, b.Kind, b.Scope, b.ParentID, b.Size, b.SHA256, storage, b.Status, b.Error, dest,
	)
	return err
}

func (db *DB) ListBackups() ([]Backup, error) {
	rows, err := db.sql.Query(`SELECT ` + backupCols + ` FROM backups ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Backup{}
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (db *DB) GetBackup(id string) (Backup, error) {
	if !ValidID(id) {
		return Backup{}, ErrNotFound
	}
	row := db.sql.QueryRow(`SELECT `+backupCols+` FROM backups WHERE id=?`, id)
	b, err := scanBackup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Backup{}, ErrNotFound
	}
	return b, err
}

func (db *DB) SetCurrentBackup(id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE backups SET current=0`); err != nil {
		tx.Rollback()
		return err
	}
	res, err := tx.Exec(`UPDATE backups SET current=1 WHERE id=?`, id)
	if err != nil {
		tx.Rollback()
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		tx.Rollback()
		return ErrNotFound
	}
	return tx.Commit()
}

func (db *DB) MarkRollbackPending() (string, []PendingRestart, error) {
	last, err := db.LastRestartAt()
	if err != nil {
		return "", nil, err
	}
	created := time.Now().UTC().Truncate(time.Second)
	if last != "" {
		prev, err := time.Parse(time.RFC3339, last)
		if err != nil {
			return "", nil, err
		}
		if !created.After(prev) {
			created = prev.Add(time.Second)
		}
	}
	id, err := NewID()
	if err != nil {
		return "", nil, err
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return "", nil, err
	}
	h := History{
		ID:        id,
		ModID:     id,
		Slug:      "server-rollback",
		Name:      "откат",
		Action:    "updated",
		Filename:  "server-rollback",
		CreatedAt: FormatTime(created),
	}
	if err := insertHistory(tx, h); err != nil {
		tx.Rollback()
		return "", nil, err
	}
	if err := tx.Commit(); err != nil {
		return "", nil, err
	}
	pending, err := db.PendingRestart(last)
	if err != nil {
		return "", nil, err
	}
	return last, pending, nil
}

func (db *DB) LatestMilestone(scope string) (Backup, error) {
	row := db.sql.QueryRow(
		`SELECT `+backupCols+` FROM backups WHERE kind=? AND scope=? ORDER BY created_at DESC, rowid DESC LIMIT 1`,
		"milestone", scope,
	)
	b, err := scanBackup(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Backup{}, ErrNotFound
	}
	return b, err
}

func (db *DB) SetBackupStorage(id, storage string) error {
	if !ValidID(id) || (storage != "disk" && storage != "s3") {
		return ErrNotFound
	}
	res, err := db.sql.Exec(`UPDATE backups SET storage=? WHERE id=?`, storage, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) PendingS3Backups() ([]Backup, error) {
	rows, err := db.sql.Query(
		`SELECT ` + backupCols + ` FROM backups WHERE kind IN ('milestone', 'snapshot') AND dest='s3' AND storage<>'s3' AND status='' ORDER BY created_at ASC, rowid ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Backup{}
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

type S3Settings struct {
	Endpoint  string
	Region    string
	Bucket    string
	Prefix    string
	AccessKey string
	Secret    string
}

type S3Public struct {
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
	AccessKey string `json:"access_key"`
	SecretSet bool   `json:"secret_set"`
	Dest      string `json:"dest"`
}

func (s S3Settings) Ready() bool {
	return strings.TrimSpace(s.Bucket) != "" && strings.TrimSpace(s.AccessKey) != "" && s.Secret != ""
}

func (db *DB) S3() (S3Settings, error) {
	var s S3Settings
	err := db.sql.QueryRow(
		`SELECT s3_endpoint, s3_region, s3_bucket, s3_prefix, s3_access_key, s3_secret FROM settings WHERE id=1`,
	).Scan(&s.Endpoint, &s.Region, &s.Bucket, &s.Prefix, &s.AccessKey, &s.Secret)
	return s, err
}

func (db *DB) S3Public() (S3Public, error) {
	s, err := db.S3()
	if err != nil {
		return S3Public{}, err
	}
	dest, err := db.BackupDest()
	if err != nil {
		return S3Public{}, err
	}
	return S3Public{
		Endpoint: s.Endpoint, Region: s.Region, Bucket: s.Bucket, Prefix: s.Prefix,
		AccessKey: s.AccessKey, SecretSet: s.Secret != "", Dest: dest,
	}, nil
}

func (db *DB) BackupDest() (string, error) {
	var dest string
	err := db.sql.QueryRow(`SELECT backup_dest FROM settings WHERE id=1`).Scan(&dest)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(dest) == "s3" {
		return "s3", nil
	}
	return "disk", nil
}

func (db *DB) SetBackupDest(dest string) error {
	if dest != "disk" && dest != "s3" {
		return ErrConflict
	}
	_, err := db.sql.Exec(`UPDATE settings SET backup_dest=? WHERE id=1`, dest)
	return err
}

func (db *DB) SaveS3(in S3Settings) error {
	in.Endpoint = strings.TrimSpace(in.Endpoint)
	in.Region = strings.TrimSpace(in.Region)
	in.Bucket = strings.TrimSpace(in.Bucket)
	in.Prefix = strings.Trim(strings.TrimSpace(in.Prefix), "/")
	in.AccessKey = strings.TrimSpace(in.AccessKey)
	if in.Secret == "" {
		_, err := db.sql.Exec(
			`UPDATE settings SET s3_endpoint=?, s3_region=?, s3_bucket=?, s3_prefix=?, s3_access_key=? WHERE id=1`,
			in.Endpoint, in.Region, in.Bucket, in.Prefix, in.AccessKey,
		)
		return err
	}
	_, err := db.sql.Exec(
		`UPDATE settings SET s3_endpoint=?, s3_region=?, s3_bucket=?, s3_prefix=?, s3_access_key=?, s3_secret=? WHERE id=1`,
		in.Endpoint, in.Region, in.Bucket, in.Prefix, in.AccessKey, in.Secret,
	)
	return err
}

func (db *DB) CountChildSnapshots(parent string) (int, error) {
	var n int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM backups WHERE parent_id=? AND kind='snapshot'`, parent).Scan(&n)
	return n, err
}

func (db *DB) DeleteBackupRow(id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	res, err := db.sql.Exec(`DELETE FROM backups WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) FailBackup(id, msg string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	_, err := db.sql.Exec(`UPDATE backups SET status='failed', error=?, current=0 WHERE id=?`, msg, id)
	return err
}

func (db *DB) FinishBackup(id string, size int64, sha string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	tx, err := db.sql.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE backups SET current=0`); err != nil {
		tx.Rollback()
		return err
	}
	res, err := tx.Exec(
		`UPDATE backups SET size=?, sha256=?, status='', error='', current=1 WHERE id=?`,
		size, sha, id,
	)
	if err != nil {
		tx.Rollback()
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		tx.Rollback()
		return ErrNotFound
	}
	return tx.Commit()
}

func (db *DB) SetBackupFingerprint(id string, size int64, sha string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	res, err := db.sql.Exec(`UPDATE backups SET size=?, sha256=? WHERE id=?`, size, sha, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanBackup(sc interface{ Scan(...any) error }) (Backup, error) {
	var b Backup
	var current int
	err := sc.Scan(&b.ID, &b.CreatedAt, &b.Label, &current, &b.Kind, &b.Scope, &b.ParentID, &b.Size, &b.SHA256, &b.Storage, &b.Status, &b.Error, &b.Dest)
	b.Current = current == 1
	return b, err
}
