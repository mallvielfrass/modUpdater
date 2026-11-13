package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (db *DB) HasRoot() (bool, error) {
	var n int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM admins WHERE root=1`).Scan(&n)
	return n > 0, err
}

func (db *DB) InsertAdmin(username, hash string, root bool) (Admin, error) {
	id, err := NewID()
	if err != nil {
		return Admin{}, err
	}
	created := Now()
	_, err = db.sql.Exec(
		`INSERT INTO admins (id, username, password_hash, root, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, username, hash, boolInt(root), created,
	)
	if err != nil {
		if isUnique(err) {
			return Admin{}, ErrUsernameTaken
		}
		return Admin{}, err
	}
	return Admin{ID: id, Username: username, Root: root, CreatedAt: created}, nil
}

func (db *DB) PasswordHash(username string) (string, error) {
	var hash string
	err := db.sql.QueryRow(`SELECT password_hash FROM admins WHERE username=?`, username).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return hash, err
}

func (db *DB) AdminByUsername(username string) (Admin, string, error) {
	var a Admin
	var root int
	var hash string
	err := db.sql.QueryRow(
		`SELECT id, username, password_hash, root, created_at FROM admins WHERE username=?`,
		username,
	).Scan(&a.ID, &a.Username, &hash, &root, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Admin{}, "", ErrNotFound
	}
	if err != nil {
		return Admin{}, "", err
	}
	a.Root = root == 1
	return a, hash, nil
}

func (db *DB) ListAdmins() ([]Admin, error) {
	rows, err := db.sql.Query(`SELECT id, username, root, created_at FROM admins ORDER BY username, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Admin{}
	for rows.Next() {
		var a Admin
		var root int
		if err := rows.Scan(&a.ID, &a.Username, &root, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Root = root == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

func (db *DB) DeleteAdmin(id string) error {
	var root int
	err := db.sql.QueryRow(`SELECT root FROM admins WHERE id=?`, id).Scan(&root)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if root == 1 {
		return ErrForbidden
	}
	res, err := db.sql.Exec(`DELETE FROM admins WHERE id=? AND root=0`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) InsertSession(token, adminID, expires string) error {
	_, err := db.sql.Exec(`INSERT INTO sessions (token, admin_id, expires_at) VALUES (?, ?, ?)`, token, adminID, expires)
	return err
}

func (db *DB) InsertRefresh(token, adminID, expires string) error {
	_, err := db.sql.Exec(`INSERT INTO refresh_tokens (token, admin_id, expires_at) VALUES (?, ?, ?)`, token, adminID, expires)
	return err
}

func (db *DB) ConsumeRefresh(token string) (Admin, string, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return Admin{}, "", err
	}
	defer tx.Rollback()
	var a Admin
	var root int
	var expires string
	err = tx.QueryRow(`
		SELECT a.id, a.username, a.root, a.created_at, r.expires_at
		FROM refresh_tokens r JOIN admins a ON a.id = r.admin_id
		WHERE r.token=?`, token).Scan(&a.ID, &a.Username, &root, &a.CreatedAt, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Admin{}, "", ErrNotFound
	}
	if err != nil {
		return Admin{}, "", err
	}
	a.Root = root == 1
	res, err := tx.Exec(`DELETE FROM refresh_tokens WHERE token=?`, token)
	if err != nil {
		return Admin{}, "", err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Admin{}, "", ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return Admin{}, "", err
	}
	return a, expires, nil
}

func (db *DB) DeleteSession(token string) error {
	_, err := db.sql.Exec(`DELETE FROM sessions WHERE token=?`, token)
	return err
}

func (db *DB) LookupSession(token string) (Admin, string, error) {
	var a Admin
	var root int
	var expires string
	err := db.sql.QueryRow(`
		SELECT a.id, a.username, a.root, a.created_at, s.expires_at
		FROM sessions s JOIN admins a ON a.id = s.admin_id
		WHERE s.token=?`, token).Scan(&a.ID, &a.Username, &root, &a.CreatedAt, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Admin{}, "", ErrNotFound
	}
	if err != nil {
		return Admin{}, "", err
	}
	a.Root = root == 1
	return a, expires, nil
}

func (db *DB) SessionCount(adminID string) (int, error) {
	var n int
	err := db.sql.QueryRow(`SELECT COUNT(*) FROM sessions WHERE admin_id=?`, adminID).Scan(&n)
	return n, err
}

func (db *DB) InsertRawInvite(token, expires string) error {
	_, err := db.sql.Exec(`INSERT INTO invites (token, expires_at) VALUES (?, ?)`, token, expires)
	return err
}

func (db *DB) InsertInvite(token, createdBy, expires string) error {
	_, err := db.sql.Exec(
		`INSERT INTO invites (token, created_by, expires_at) VALUES (?, ?, ?)`,
		token, createdBy, expires,
	)
	return err
}

func (db *DB) DeleteUnusedInvite(token string) (int64, error) {
	res, err := db.sql.Exec(`DELETE FROM invites WHERE token=? AND used_at IS NULL`, token)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (db *DB) RegisterTx(username, hash, invite string, now time.Time) (Admin, string, string, string, error) {
	ctx := context.Background()
	conn, err := db.sql.Conn(ctx)
	if err != nil {
		return Admin{}, "", "", "", err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return Admin{}, "", "", "", err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()
	nowS := FormatTime(now)
	var one int
	err = conn.QueryRowContext(ctx,
		`SELECT 1 FROM invites WHERE token=? AND used_at IS NULL AND expires_at > ?`,
		invite, nowS,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return Admin{}, "", "", "", ErrInviteInvalid
	}
	if err != nil {
		return Admin{}, "", "", "", err
	}
	id, err := NewID()
	if err != nil {
		return Admin{}, "", "", "", err
	}
	_, err = conn.ExecContext(ctx,
		`INSERT INTO admins (id, username, password_hash, root, created_at) VALUES (?, ?, ?, 0, ?)`,
		id, username, hash, nowS,
	)
	if err != nil {
		if isUnique(err) {
			return Admin{}, "", "", "", ErrUsernameTaken
		}
		return Admin{}, "", "", "", err
	}
	res, err := conn.ExecContext(ctx,
		`UPDATE invites SET used_by=?, used_at=? WHERE token=? AND used_at IS NULL AND expires_at > ?`,
		id, nowS, invite, nowS,
	)
	if err != nil {
		return Admin{}, "", "", "", err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Admin{}, "", "", "", ErrInviteInvalid
	}
	tok, err := NewToken()
	if err != nil {
		return Admin{}, "", "", "", err
	}
	ref, err := NewToken()
	if err != nil {
		return Admin{}, "", "", "", err
	}
	exp := FormatTime(now.Add(30 * 24 * time.Hour))
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO sessions (token, admin_id, expires_at) VALUES (?, ?, ?)`,
		tok, id, exp,
	); err != nil {
		return Admin{}, "", "", "", err
	}
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO refresh_tokens (token, admin_id, expires_at) VALUES (?, ?, ?)`,
		ref, id, exp,
	); err != nil {
		return Admin{}, "", "", "", err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return Admin{}, "", "", "", err
	}
	committed = true
	return Admin{ID: id, Username: username, Root: false, CreatedAt: nowS}, tok, ref, exp, nil
}
