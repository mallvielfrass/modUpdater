package store

import (
	"database/sql"
	_ "embed"
	"strings"
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
