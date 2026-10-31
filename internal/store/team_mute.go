package store

// teamMutesSchema keeps the members whose mail waits until it is delivered by
// hand, so two that write to each other without end can be quietened. Like
// the trash, it can run twice.
const teamMutesSchema = `CREATE TABLE IF NOT EXISTS team_mutes (
		team_id    TEXT NOT NULL,
		role       TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (team_id, role)
	);`

// Mute stops hive from typing a member's queued mail into its pane. The mail
// still queues, and `hive team inbox` still reads it.
func (s *Store) Mute(teamID, role string, at int64) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO team_mutes (team_id, role, created_at) VALUES (?, ?, ?)`,
		teamID, role, at)
	return err
}

// Unmute lets hive deliver a member's mail again.
func (s *Store) Unmute(teamID, role string) error {
	_, err := s.db.Exec(`DELETE FROM team_mutes WHERE team_id = ? AND role = ?`, teamID, role)
	return err
}

// Muted reports whether a member's mail waits to be delivered by hand.
func (s *Store) Muted(teamID, role string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM team_mutes WHERE team_id = ? AND role = ?`, teamID, role).Scan(&n)
	return n > 0, err
}

// Mutes returns the roles whose mail is muted, by role.
func (s *Store) Mutes(teamID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT role FROM team_mutes WHERE team_id = ? ORDER BY role`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		out = append(out, role)
	}
	return out, rows.Err()
}
