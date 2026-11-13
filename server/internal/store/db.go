package store

import (
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"math/big"
	"net/url"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/001_init.sql
var migration001 string

//go:embed migrations/002_features.sql
var migration002 string

//go:embed migrations/003_refresh.sql
var migration003 string

//go:embed migrations/004_created_at.sql
var migration004 string

//go:embed migrations/005_revoked.sql
var migration005 string

//go:embed migrations/006_client_required.sql
var migration006 string

//go:embed migrations/007_backups.sql
var migration007 string

//go:embed migrations/008_daily_backup.sql
var migration008 string

//go:embed migrations/009_backup_scope.sql
var migration009 string

//go:embed migrations/010_backup_kind.sql
var migration010 string

//go:embed migrations/011_s3.sql
var migration011 string

//go:embed migrations/012_backup_status.sql
var migration012 string

//go:embed migrations/013_backup_dest.sql
var migration013 string

//go:embed migrations/014_changelog.sql
var migration014 string

var (
	ErrNotFound      = errors.New("not_found")
	ErrForbidden     = errors.New("forbidden")
	ErrConflict      = errors.New("conflict")
	ErrInviteInvalid = errors.New("invite_invalid")
	ErrUsernameTaken = errors.New("username_taken")

	idRe    = regexp.MustCompile(`^[0-9a-f]{32}$`)
	tokenRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type DB struct {
	sql *sql.DB
}

type Admin struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Root      bool   `json:"root"`
	CreatedAt string `json:"created_at"`
}

type Mod struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Filename       string `json:"filename"`
	Version        string `json:"version"`
	SHA256         string `json:"sha256"`
	Size           int64  `json:"size"`
	Client         bool   `json:"client"`
	ClientOnly     bool   `json:"client_only"`
	ClientRequired bool   `json:"client_required"`
	Revoked        bool   `json:"revoked"`
	UpdatedAt      string `json:"updated_at"`
	CreatedAt      string `json:"created_at"`
}

type History struct {
	ID          string
	ModID       string
	Slug        string
	Name        string
	Action      string
	Filename    string
	Version     string
	SHA256      string
	Size        int64
	Client      bool
	ClientOnly  bool
	ArchiveName string
	CreatedAt   string
}

func Open(path string) (*DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	sqlDB, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	if _, err := sqlDB.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		sqlDB.Close()
		return nil, err
	}
	var mode string
	if err := sqlDB.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&mode); err != nil {
		sqlDB.Close()
		return nil, err
	}
	if _, err := sqlDB.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		sqlDB.Close()
		return nil, err
	}
	db := &DB{sql: sqlDB}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.sql.Close()
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func execScript(ex execer, script string) error {
	for _, stmt := range strings.Split(script, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := ex.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func setUserVersion(ex execer, v int) error {
	q := `PRAGMA user_version = 1`
	switch v {
	case 2:
		q = `PRAGMA user_version = 2`
	case 3:
		q = `PRAGMA user_version = 3`
	case 4:
		q = `PRAGMA user_version = 4`
	case 5:
		q = `PRAGMA user_version = 5`
	case 6:
		q = `PRAGMA user_version = 6`
	case 7:
		q = `PRAGMA user_version = 7`
	case 8:
		q = `PRAGMA user_version = 8`
	case 9:
		q = `PRAGMA user_version = 9`
	case 10:
		q = `PRAGMA user_version = 10`
	case 11:
		q = `PRAGMA user_version = 11`
	case 12:
		q = `PRAGMA user_version = 12`
	case 13:
		q = `PRAGMA user_version = 13`
	case 14:
		q = `PRAGMA user_version = 14`
	}
	_, err := ex.Exec(q)
	return err
}

func (db *DB) migrate() error {
	var version int
	if err := db.sql.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version == 0 {
		var n int
		if err := db.sql.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='admins'`).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if err := execScript(db.sql, migration001); err != nil {
				return err
			}
		}
		if err := setUserVersion(db.sql, 1); err != nil {
			return err
		}
		version = 1
	}
	if version < 2 {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		if err := execScript(tx, migration002); err != nil {
			tx.Rollback()
			return err
		}
		if err := setUserVersion(tx, 2); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = 2
	}
	if version < 3 {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		if err := execScript(tx, migration003); err != nil {
			tx.Rollback()
			return err
		}
		if err := setUserVersion(tx, 3); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = 3
	}
	if version < 4 {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		if err := execScript(tx, migration004); err != nil {
			tx.Rollback()
			return err
		}
		if err := setUserVersion(tx, 4); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = 4
	}
	if version < 5 {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		if err := execScript(tx, migration005); err != nil {
			tx.Rollback()
			return err
		}
		if err := setUserVersion(tx, 5); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = 5
	}
	if version < 6 {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		if err := execScript(tx, migration006); err != nil {
			tx.Rollback()
			return err
		}
		if err := setUserVersion(tx, 6); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = 6
	}
	if version < 7 {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		if err := execScript(tx, migration007); err != nil {
			tx.Rollback()
			return err
		}
		if err := setUserVersion(tx, 7); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = 7
	}
	if version < 8 {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		if err := execScript(tx, migration008); err != nil {
			tx.Rollback()
			return err
		}
		if err := setUserVersion(tx, 8); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = 8
	}
	if version < 9 {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		if err := execScript(tx, migration009); err != nil {
			tx.Rollback()
			return err
		}
		if err := setUserVersion(tx, 9); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = 9
	}
	if version < 10 {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		if err := execScript(tx, migration010); err != nil {
			tx.Rollback()
			return err
		}
		if err := setUserVersion(tx, 10); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = 10
	}
	if version < 11 {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		if err := execScript(tx, migration011); err != nil {
			tx.Rollback()
			return err
		}
		if err := setUserVersion(tx, 11); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = 11
	}
	if version < 12 {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		if err := execScript(tx, migration012); err != nil {
			tx.Rollback()
			return err
		}
		if err := setUserVersion(tx, 12); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = 12
	}
	if version < 13 {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		if err := execScript(tx, migration013); err != nil {
			tx.Rollback()
			return err
		}
		if err := setUserVersion(tx, 13); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		version = 13
	}
	if version < 14 {
		tx, err := db.sql.Begin()
		if err != nil {
			return err
		}
		if err := execScript(tx, migration014); err != nil {
			tx.Rollback()
			return err
		}
		if err := setUserVersion(tx, 14); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func NewID() (string, error) {
	return randomHex(16)
}

func NewToken() (string, error) {
	return randomHex(32)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func ValidID(s string) bool {
	return idRe.MatchString(s)
}

func ValidToken(s string) bool {
	return tokenRe.MatchString(s)
}

func FormatTime(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}

func Now() string {
	return FormatTime(time.Now())
}

func GeneratePassword(n int) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	max := big.NewInt(int64(len(alphabet)))
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[v.Int64()]
	}
	return string(out), nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE")
}
