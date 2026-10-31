package store

// teamTrashSchema marks the teams in the trash. Their members stay, so a
// restore brings the team back as it was; a purge deletes them through their
// tools. Like the trash, it can run twice.
const teamTrashSchema = `CREATE TABLE IF NOT EXISTS team_trash (
		team_id TEXT PRIMARY KEY,
		at      INTEGER NOT NULL
	);`

// TrashTeam moves a team to the trash.
func (s *Store) TrashTeam(teamID string, at int64) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO team_trash (team_id, at) VALUES (?, ?)`, teamID, at)
	return err
}

// RestoreTeam takes a team out of the trash.
func (s *Store) RestoreTeam(teamID string) error {
	_, err := s.db.Exec(`DELETE FROM team_trash WHERE team_id = ?`, teamID)
	return err
}

// TeamTrashed reports whether a team is in the trash.
func (s *Store) TeamTrashed(teamID string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM team_trash WHERE team_id = ?`, teamID).Scan(&n)
	return n > 0, err
}

// TrashedTeams returns the teams in the trash, most recently trashed first.
func (s *Store) TrashedTeams() ([]Team, error) {
	rows, err := s.db.Query(`SELECT ` + teamColumns + ` FROM teams WHERE id IN (SELECT team_id FROM team_trash)
		ORDER BY (SELECT at FROM team_trash WHERE team_id = teams.id) DESC, id`)
	if err != nil {
		return nil, err
	}
	return scanTeams(rows)
}
