package store

import (
	"database/sql"
	"errors"
)

type ModConfig struct {
	ID        string `json:"id"`
	ModID     string `json:"-"`
	Dest      string `json:"dest"`
	Kind      string `json:"kind"`
	Filename  string `json:"filename"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	UpdatedAt string `json:"updated_at"`
}

func scanConfig(sc interface{ Scan(...any) error }) (ModConfig, error) {
	var c ModConfig
	err := sc.Scan(&c.ID, &c.ModID, &c.Dest, &c.Kind, &c.Filename, &c.SHA256, &c.Size, &c.UpdatedAt)
	return c, err
}

const configCols = `id, mod_id, dest, kind, filename, sha256, size, updated_at`

func (db *DB) ListConfigs(modID string) ([]ModConfig, error) {
	rows, err := db.sql.Query(`SELECT `+configCols+` FROM mod_configs WHERE mod_id=? ORDER BY dest, id`, modID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectConfigs(rows)
}

func (db *DB) ConfigsByMod() (map[string][]ModConfig, error) {
	rows, err := db.sql.Query(`SELECT ` + configCols + ` FROM mod_configs ORDER BY dest, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list, err := collectConfigs(rows)
	if err != nil {
		return nil, err
	}
	out := map[string][]ModConfig{}
	for _, c := range list {
		out[c.ModID] = append(out[c.ModID], c)
	}
	return out, nil
}

func collectConfigs(rows *sql.Rows) ([]ModConfig, error) {
	out := []ModConfig{}
	for rows.Next() {
		c, err := scanConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (db *DB) GetConfig(modID, id string) (ModConfig, error) {
	row := db.sql.QueryRow(`SELECT `+configCols+` FROM mod_configs WHERE id=? AND mod_id=?`, id, modID)
	c, err := scanConfig(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ModConfig{}, ErrNotFound
	}
	return c, err
}

func (db *DB) InsertConfig(c ModConfig) error {
	_, err := db.sql.Exec(
		`INSERT INTO mod_configs (id, mod_id, dest, kind, filename, sha256, size, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.ModID, c.Dest, c.Kind, c.Filename, c.SHA256, c.Size, c.UpdatedAt,
	)
	return err
}

func (db *DB) DeleteConfig(modID, id string) error {
	res, err := db.sql.Exec(`DELETE FROM mod_configs WHERE id=? AND mod_id=?`, id, modID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
