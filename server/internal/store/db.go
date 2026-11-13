package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"math/big"
	"net/url"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"modupdater/internal/modlib"
)

var (
	ErrNotFound      = errors.New("not_found")
	ErrForbidden     = errors.New("forbidden")
	ErrConflict      = errors.New("conflict")
	ErrInviteInvalid = errors.New("invite_invalid")
	ErrUsernameTaken = errors.New("username_taken")

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
	return modlib.ValidID(s)
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
