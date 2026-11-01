package store

import (
	"database/sql"
	"errors"
)

// Quota is a tool account's rate-limit windows, as the tool last reported
// them. The percents are 0 to 100, and the resets are unix seconds, 0 when
// the tool didn't say.
type Quota struct {
	Tool       string  `json:"tool"`
	FiveUsed   float64 `json:"five_used"`
	FiveReset  int64   `json:"five_resets"`
	SevenUsed  float64 `json:"seven_used"`
	SevenReset int64   `json:"seven_resets"`
	At         int64   `json:"at"` // when the tool reported it
}

// Empty reports whether the tool said nothing about its windows.
func (q Quota) Empty() bool {
	return q.FiveUsed == 0 && q.FiveReset == 0 && q.SevenUsed == 0 && q.SevenReset == 0
}

const quotaSchema = `CREATE TABLE IF NOT EXISTS quota_limits (
		tool         TEXT PRIMARY KEY,
		five_used    REAL NOT NULL DEFAULT 0,
		five_resets  INTEGER NOT NULL DEFAULT 0,
		seven_used   REAL NOT NULL DEFAULT 0,
		seven_resets INTEGER NOT NULL DEFAULT 0,
		at           INTEGER NOT NULL DEFAULT 0
	);`

// SetQuota records a tool's rate-limit windows, replacing what it said last.
func (s *Store) SetQuota(q Quota) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO quota_limits
		(tool, five_used, five_resets, seven_used, seven_resets, at) VALUES (?, ?, ?, ?, ?, ?)`,
		q.Tool, q.FiveUsed, q.FiveReset, q.SevenUsed, q.SevenReset, q.At)
	return err
}

// Quota returns one tool's last reported windows.
func (s *Store) Quota(tool string) (Quota, bool, error) {
	var q Quota
	err := s.db.QueryRow(`SELECT tool, five_used, five_resets, seven_used, seven_resets, at FROM quota_limits WHERE tool = ?`,
		tool).Scan(&q.Tool, &q.FiveUsed, &q.FiveReset, &q.SevenUsed, &q.SevenReset, &q.At)
	if errors.Is(err, sql.ErrNoRows) {
		return Quota{}, false, nil
	}
	return q, err == nil, err
}

// Quotas returns every tool's last reported windows, by tool.
func (s *Store) Quotas() (map[string]Quota, error) {
	rows, err := s.db.Query(`SELECT tool, five_used, five_resets, seven_used, seven_resets, at FROM quota_limits`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Quota{}
	for rows.Next() {
		var q Quota
		if err := rows.Scan(&q.Tool, &q.FiveUsed, &q.FiveReset, &q.SevenUsed, &q.SevenReset, &q.At); err != nil {
			return nil, err
		}
		out[q.Tool] = q
	}
	return out, rows.Err()
}
