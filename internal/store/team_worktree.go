package store

import "database/sql"

// WorkspaceWorktrees is a team whose members each work in a git worktree and
// branch of their own, while the lead works in the team's folder and merges.
const WorkspaceWorktrees = "worktrees"

// teamWorktreesSchema keeps what a worktrees team needs to clean up after
// itself: where it started from, and the worktrees hive made. Like the trash,
// it can run twice.
const teamWorktreesSchema = `CREATE TABLE IF NOT EXISTS team_bases (
		team_id    TEXT PRIMARY KEY,
		branch     TEXT NOT NULL DEFAULT '',
		head       TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS team_worktrees (
		team_id    TEXT NOT NULL,
		role       TEXT NOT NULL,
		path       TEXT NOT NULL,
		branch     TEXT NOT NULL,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (team_id, role)
	);`

// TeamBase is where a worktrees team started: the branch its folder had,
// and the commit the first worktrees were made from.
type TeamBase struct {
	TeamID    string `json:"team_id"`
	Branch    string `json:"branch,omitempty"` // "" when the folder's HEAD was detached
	Head      string `json:"head"`
	CreatedAt int64  `json:"created_at"`
}

// Worktree is a git worktree hive made for a member of a team.
type Worktree struct {
	TeamID    string `json:"team_id"`
	Role      string `json:"role"`
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	CreatedAt int64  `json:"created_at"`
}

// PutTeamBase records where a worktrees team started.
func (s *Store) PutTeamBase(b TeamBase) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO team_bases (team_id, branch, head, created_at) VALUES (?, ?, ?, ?)`,
		b.TeamID, b.Branch, b.Head, b.CreatedAt)
	return err
}

// TeamBase returns where a worktrees team started.
func (s *Store) TeamBase(teamID string) (TeamBase, bool, error) {
	b := TeamBase{TeamID: teamID}
	err := s.db.QueryRow(`SELECT branch, head, created_at FROM team_bases WHERE team_id = ?`, teamID).
		Scan(&b.Branch, &b.Head, &b.CreatedAt)
	if err == sql.ErrNoRows {
		return b, false, nil
	}
	return b, err == nil, err
}

// PutWorktree records a member's worktree.
func (s *Store) PutWorktree(w Worktree) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO team_worktrees (team_id, role, path, branch, created_at) VALUES (?, ?, ?, ?, ?)`,
		w.TeamID, w.Role, w.Path, w.Branch, w.CreatedAt)
	return err
}

// Worktrees returns the worktrees a team still has, in the order they were
// made.
func (s *Store) Worktrees(teamID string) ([]Worktree, error) {
	rows, err := s.db.Query(`SELECT team_id, role, path, branch, created_at FROM team_worktrees
		WHERE team_id = ? ORDER BY created_at, rowid`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Worktree
	for rows.Next() {
		var w Worktree
		if err := rows.Scan(&w.TeamID, &w.Role, &w.Path, &w.Branch, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ForgetWorktree drops the record of a worktree that is gone.
func (s *Store) ForgetWorktree(teamID, role string) error {
	_, err := s.db.Exec(`DELETE FROM team_worktrees WHERE team_id = ? AND role = ?`, teamID, role)
	return err
}

// ForgetWorktrees drops a team's worktree records and its base, for a team
// that is going away.
func (s *Store) ForgetWorktrees(teamID string) error {
	if _, err := s.db.Exec(`DELETE FROM team_worktrees WHERE team_id = ?`, teamID); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM team_bases WHERE team_id = ?`, teamID)
	return err
}

// SetMemberCwd records the folder a member works in.
func (s *Store) SetMemberCwd(teamID, role, cwd string) error {
	_, err := s.db.Exec(`UPDATE team_members SET cwd = ? WHERE team_id = ? AND role = ?`, cwd, teamID, role)
	return err
}

// DropQueued forgets the messages still queued for a team's members, and
// returns how many there were: a done team delivers nothing more.
func (s *Store) DropQueued(teamID string) (int, error) {
	res, err := s.db.Exec(`DELETE FROM team_deliveries WHERE delivered_at = 0 AND message_id IN
		(SELECT id FROM team_messages WHERE team_id = ?)`, teamID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
