package store

import "sort"

type PendingRestart struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Filename  string `json:"filename"`
	Action    string `json:"action"`
	CreatedAt string `json:"created_at"`
}

func (db *DB) PendingRestart(last string) ([]PendingRestart, error) {
	q := `SELECT slug, name, filename, action, created_at FROM history WHERE client_only=0`
	var args []any
	if last != "" {
		q += ` AND created_at > ?`
		args = append(args, last)
	}
	q += ` ORDER BY created_at ASC, rowid ASC`
	rows, err := db.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	latest := map[string]PendingRestart{}
	var slugs []string
	for rows.Next() {
		var p PendingRestart
		if err := rows.Scan(&p.Slug, &p.Name, &p.Filename, &p.Action, &p.CreatedAt); err != nil {
			return nil, err
		}
		if _, ok := latest[p.Slug]; !ok {
			slugs = append(slugs, p.Slug)
		}
		latest[p.Slug] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]PendingRestart, 0, len(slugs))
	for _, slug := range slugs {
		out = append(out, latest[slug])
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].Slug < out[j].Slug
		}
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out, nil
}
