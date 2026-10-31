package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// A team's workspace.
const WorkspaceShared = "shared"

// Statuses of a team.
const (
	TeamActive = "active"
	TeamDone   = "done"
)

// Kinds of channel message.
const (
	MsgPost   = "post"   // to the channel; typed into no pane
	MsgSay    = "say"    // to a teammate, or everyone; typed into their panes
	MsgSystem = "system" // from hive, about what it saw
)

// Who a message can come from or go to, besides a member's role.
const (
	FromYou  = "you"
	FromHive = "hive"
	ToAll    = "all"
)

// SourceTeam marks the stand-in session the tree makes for a team node.
const SourceTeam = "team"

// Team is a named group of agents working on one goal in one folder.
type Team struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Goal      string `json:"goal,omitempty"`
	Cwd       string `json:"cwd"`
	Workspace string `json:"workspace"`
	CreatedBy string `json:"created_by,omitempty"` // the session that ran `hive team new`, if an agent did
	Status    string `json:"status"`
	Paused    bool   `json:"paused,omitempty"`
	LeadRole  string `json:"lead,omitempty"` // "" when the user leads
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// Member is one role on a team and the session that plays it now.
type Member struct {
	TeamID    string `json:"team_id"`
	Role      string `json:"role"`
	SessionID string `json:"session_id,omitempty"` // moves when the member is resumed under a new ID
	Tool      string `json:"tool"`
	Model     string `json:"model,omitempty"`
	Prompt    string `json:"prompt,omitempty"` // the member's own part of the brief
	Cwd       string `json:"cwd,omitempty"`
	Pane      string `json:"pane,omitempty"` // where it was last started, until its session is known
	JoinedAt  int64  `json:"joined_at"`
	LeftAt    int64  `json:"left_at,omitempty"` // retired with `hive team rm`
}

// Active reports whether the member is still on the team.
func (m Member) Active() bool { return m.LeftAt == 0 }

// Message is one line of a team's channel.
type Message struct {
	ID      int64  `json:"id"`
	TeamID  string `json:"team_id"`
	From    string `json:"from"` // a role, FromYou or FromHive
	To      string `json:"to"`   // a role, ToAll, or "" for the channel
	Body    string `json:"body"`
	Kind    string `json:"kind"`
	Private bool   `json:"private,omitempty"` // left out of the feed the user reads
	At      int64  `json:"at"`
}

const teamsSchema = `CREATE TABLE IF NOT EXISTS teams (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		goal       TEXT NOT NULL DEFAULT '',
		cwd        TEXT NOT NULL,
		workspace  TEXT NOT NULL DEFAULT 'shared',
		created_by TEXT NOT NULL DEFAULT '',
		status     TEXT NOT NULL DEFAULT 'active',
		paused     INTEGER NOT NULL DEFAULT 0,
		lead_role  TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS team_members (
		team_id    TEXT NOT NULL,
		role       TEXT NOT NULL,
		session_id TEXT NOT NULL DEFAULT '',
		tool       TEXT NOT NULL,
		model      TEXT NOT NULL DEFAULT '',
		prompt     TEXT NOT NULL DEFAULT '',
		cwd        TEXT NOT NULL DEFAULT '',
		pane       TEXT NOT NULL DEFAULT '',
		joined_at  INTEGER NOT NULL,
		left_at    INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (team_id, role)
	);
	CREATE INDEX IF NOT EXISTS team_members_session ON team_members(session_id);
	CREATE TABLE IF NOT EXISTS team_messages (
		id        INTEGER PRIMARY KEY,
		team_id   TEXT NOT NULL,
		from_role TEXT NOT NULL,
		to_role   TEXT NOT NULL DEFAULT '',
		body      TEXT NOT NULL,
		kind      TEXT NOT NULL DEFAULT 'post',
		private   INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS team_messages_team ON team_messages(team_id, id);
	CREATE TABLE IF NOT EXISTS team_deliveries (
		message_id   INTEGER NOT NULL,
		role         TEXT NOT NULL,
		delivered_at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (message_id, role)
	);
	CREATE TABLE IF NOT EXISTS launch_teams (
		pane       TEXT NOT NULL,
		team_id    TEXT NOT NULL,
		role       TEXT NOT NULL,
		pid        INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		PRIMARY KEY (pane, team_id)
	);
	CREATE TABLE IF NOT EXISTS team_reads (
		team_id TEXT PRIMARY KEY,
		last_id INTEGER NOT NULL
	);`

const teamColumns = `id, name, goal, cwd, workspace, created_by, status, paused, lead_role, created_at, updated_at`

func scanTeams(rows *sql.Rows) ([]Team, error) {
	defer rows.Close()
	var out []Team
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Name, &t.Goal, &t.Cwd, &t.Workspace, &t.CreatedBy, &t.Status,
			&t.Paused, &t.LeadRole, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateTeam records a team. Names are unique among teams.
func (s *Store) CreateTeam(t Team) error {
	if t.Workspace == "" {
		t.Workspace = WorkspaceShared
	}
	if t.Status == "" {
		t.Status = TeamActive
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM teams WHERE name = ?`, t.Name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("there is a team called %q already", t.Name)
	}
	if _, err := tx.Exec(`INSERT INTO teams (`+teamColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Goal, t.Cwd, t.Workspace, t.CreatedBy, t.Status, t.Paused, t.LeadRole, t.CreatedAt, t.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// Teams returns every team, newest first.
func (s *Store) Teams() ([]Team, error) {
	rows, err := s.db.Query(`SELECT ` + teamColumns + ` FROM teams ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	return scanTeams(rows)
}

// TeamByID returns the team with exactly this ID.
func (s *Store) TeamByID(id string) (Team, error) {
	rows, err := s.db.Query(`SELECT `+teamColumns+` FROM teams WHERE id = ?`, id)
	if err != nil {
		return Team{}, err
	}
	teams, err := scanTeams(rows)
	if err != nil {
		return Team{}, err
	}
	if len(teams) == 0 {
		return Team{}, fmt.Errorf("no team %s", id)
	}
	return teams[0], nil
}

// DeleteTeam forgets a team with its members and its channel.
func (s *Store) DeleteTeam(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM team_deliveries WHERE message_id IN (SELECT id FROM team_messages WHERE team_id = ?)`,
		`DELETE FROM team_messages WHERE team_id = ?`,
		`DELETE FROM team_members WHERE team_id = ?`,
		`DELETE FROM launch_teams WHERE team_id = ?`,
		`DELETE FROM team_reads WHERE team_id = ?`,
		`DELETE FROM teams WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Team finds a team by its ID, its name, or a prefix of either that only one
// team has.
func (s *Store) Team(ref string) (Team, error) {
	ref = strings.TrimSpace(ref)
	teams, err := s.Teams()
	if err != nil {
		return Team{}, err
	}
	for _, t := range teams {
		if t.ID == ref || t.Name == ref {
			return t, nil
		}
	}
	var found []Team
	for _, t := range teams {
		if ref != "" && (strings.HasPrefix(t.ID, ref) || strings.HasPrefix(t.Name, ref)) {
			found = append(found, t)
		}
	}
	switch len(found) {
	case 0:
		return Team{}, fmt.Errorf("no team matches %q; `hive team ls` lists them", ref)
	case 1:
		return found[0], nil
	}
	return Team{}, fmt.Errorf("%q matches %d teams", ref, len(found))
}

// SetPaused pauses or resumes a team.
func (s *Store) SetPaused(id string, paused bool, at int64) error {
	_, err := s.db.Exec(`UPDATE teams SET paused = ?, updated_at = MAX(updated_at, ?) WHERE id = ?`, paused, at, id)
	return err
}

// SetLead records who leads: a role, or "" for the user.
func (s *Store) SetLead(id, role string, at int64) error {
	_, err := s.db.Exec(`UPDATE teams SET lead_role = ?, updated_at = MAX(updated_at, ?) WHERE id = ?`, role, at, id)
	return err
}

// SetTeamStatus marks a team active or done.
func (s *Store) SetTeamStatus(id, status string, at int64) error {
	_, err := s.db.Exec(`UPDATE teams SET status = ?, updated_at = MAX(updated_at, ?) WHERE id = ?`, status, at, id)
	return err
}

const memberColumns = `team_id, role, session_id, tool, model, prompt, cwd, pane, joined_at, left_at`

func scanMembers(rows *sql.Rows) ([]Member, error) {
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.TeamID, &m.Role, &m.SessionID, &m.Tool, &m.Model, &m.Prompt, &m.Cwd, &m.Pane,
			&m.JoinedAt, &m.LeftAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddMember puts a member on a team. A role retired earlier is taken again.
func (s *Store) AddMember(m Member) error {
	var live int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM team_members WHERE team_id = ? AND role = ? AND left_at = 0`,
		m.TeamID, m.Role).Scan(&live); err != nil {
		return err
	}
	if live > 0 {
		return fmt.Errorf("the team has a %s already", m.Role)
	}
	_, err := s.db.Exec(`INSERT OR REPLACE INTO team_members (`+memberColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		m.TeamID, m.Role, m.SessionID, m.Tool, m.Model, m.Prompt, m.Cwd, m.Pane, m.JoinedAt)
	return err
}

// Members returns a team's members in the order they joined, retired ones
// too. Members that joined together keep the order they were added in: the
// lead first.
func (s *Store) Members(teamID string) ([]Member, error) {
	rows, err := s.db.Query(`SELECT `+memberColumns+` FROM team_members WHERE team_id = ?
		ORDER BY joined_at, rowid`, teamID)
	if err != nil {
		return nil, err
	}
	return scanMembers(rows)
}

// AllMembers returns the members of every team still active.
func (s *Store) AllMembers() ([]Member, error) {
	rows, err := s.db.Query(`SELECT ` + memberColumns + ` FROM team_members WHERE left_at = 0 ORDER BY team_id, joined_at, rowid`)
	if err != nil {
		return nil, err
	}
	return scanMembers(rows)
}

// Memberships returns the members a session plays, newest first: one on
// each team it is on. An agent can lead a team of its own (--lead self)
// while it is a member of another.
func (s *Store) Memberships(sessionID string) ([]Member, error) {
	if sessionID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT `+memberColumns+` FROM team_members WHERE session_id = ? AND left_at = 0
		ORDER BY joined_at DESC, team_id`, sessionID)
	if err != nil {
		return nil, err
	}
	return scanMembers(rows)
}

// SetMemberSession records the session now playing a role, and the pane it
// was started in.
func (s *Store) SetMemberSession(teamID, role, sessionID, pane string) error {
	_, err := s.db.Exec(`UPDATE team_members SET session_id = ?, pane = CASE WHEN ? <> '' THEN ? ELSE pane END
		WHERE team_id = ? AND role = ?`, sessionID, pane, pane, teamID, role)
	return err
}

// SetMemberTool records the tool and model a member was started with, when
// hive picked them.
func (s *Store) SetMemberTool(teamID, role, tool, model string) error {
	_, err := s.db.Exec(`UPDATE team_members SET tool = ?, model = ? WHERE team_id = ? AND role = ?`, tool, model, teamID, role)
	return err
}

// SetMemberPane records where a member was last started.
func (s *Store) SetMemberPane(teamID, role, pane string) error {
	_, err := s.db.Exec(`UPDATE team_members SET pane = ? WHERE team_id = ? AND role = ?`, pane, teamID, role)
	return err
}

// RetireMember takes a member off the team, keeping its history.
func (s *Store) RetireMember(teamID, role string, at int64) error {
	res, err := s.db.Exec(`UPDATE team_members SET left_at = ? WHERE team_id = ? AND role = ? AND left_at = 0`,
		at, teamID, role)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("the team has no %s", role)
	}
	_, err = s.db.Exec(`DELETE FROM team_deliveries WHERE role = ? AND delivered_at = 0 AND message_id IN
		(SELECT id FROM team_messages WHERE team_id = ?)`, role, teamID)
	return err
}

// LaunchTeam records that the next session to report in from a pane plays
// a role: the agent hive just started there, or, when PID is set, the next
// session of the process that ran the role until it ended.
type LaunchTeam struct {
	Pane      string
	TeamID    string
	Role      string
	PID       int
	CreatedAt int64
}

// PutLaunchTeam remembers a launch record, replacing the pane's earlier one
// for the same team.
func (s *Store) PutLaunchTeam(l LaunchTeam) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO launch_teams (pane, team_id, role, pid, created_at) VALUES (?, ?, ?, ?, ?)`,
		l.Pane, l.TeamID, l.Role, l.PID, l.CreatedAt)
	return err
}

// TakeLaunchTeams returns and forgets pane's launch records made after
// since. It forgets older ones anywhere, unreturned: they belong to agents
// that never reported in, and a pane ID comes back after tmux restarts.
func (s *Store) TakeLaunchTeams(pane string, since int64) ([]LaunchTeam, error) {
	rows, err := s.db.Query(`DELETE FROM launch_teams WHERE pane = ? OR created_at < ?
		RETURNING pane, team_id, role, pid, created_at`, pane, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LaunchTeam
	for rows.Next() {
		var l LaunchTeam
		if err := rows.Scan(&l.Pane, &l.TeamID, &l.Role, &l.PID, &l.CreatedAt); err != nil {
			return nil, err
		}
		if l.Pane == pane && l.CreatedAt >= since {
			out = append(out, l)
		}
	}
	return out, rows.Err()
}

// AddMessage appends a message to a team's channel, queueing it for each of
// recipients (roles). It returns the stored message.
func (s *Store) AddMessage(m Message, recipients []string) (Message, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO team_messages (team_id, from_role, to_role, body, kind, private, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, m.TeamID, m.From, m.To, m.Body, m.Kind, m.Private, m.At)
	if err != nil {
		return Message{}, err
	}
	m.ID, _ = res.LastInsertId()
	for _, r := range recipients {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO team_deliveries (message_id, role) VALUES (?, ?)`, m.ID, r); err != nil {
			return Message{}, err
		}
	}
	if _, err := tx.Exec(`UPDATE teams SET updated_at = MAX(updated_at, ?) WHERE id = ?`, m.At, m.TeamID); err != nil {
		return Message{}, err
	}
	return m, tx.Commit()
}

const messageColumns = `id, team_id, from_role, to_role, body, kind, private, created_at`

func scanMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.TeamID, &m.From, &m.To, &m.Body, &m.Kind, &m.Private, &m.At); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Messages returns the last n messages of a team's channel, oldest first.
// Private ones are left out unless all is set.
func (s *Store) Messages(teamID string, n int, all bool) ([]Message, error) {
	q := `SELECT ` + messageColumns + ` FROM team_messages WHERE team_id = ?`
	if !all {
		q += ` AND private = 0`
	}
	rows, err := s.db.Query(`SELECT * FROM (`+q+` ORDER BY id DESC LIMIT ?) ORDER BY id`, teamID, n)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// Pending returns the messages queued for a role and not delivered, oldest first.
func (s *Store) Pending(teamID, role string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT `+messageColumns+` FROM team_messages
		WHERE team_id = ? AND id IN (SELECT message_id FROM team_deliveries WHERE role = ? AND delivered_at = 0)
		ORDER BY id`, teamID, role)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// ClaimPending marks a role's queued messages delivered as of at and returns
// them, so that two processes delivering at once (the sender's, and the hook
// that sees the agent go idle) don't type them twice. Give them back with
// Unclaim if typing fails.
func (s *Store) ClaimPending(teamID, role string, at int64) ([]Message, error) {
	// One statement, so a competing claim sees all of it or none.
	rows, err := s.db.Query(`UPDATE team_deliveries SET delivered_at = ?
		WHERE role = ? AND delivered_at = 0 AND message_id IN (SELECT id FROM team_messages WHERE team_id = ?)
		RETURNING message_id`, at, role, teamID)
	if err != nil {
		return nil, err
	}
	var ids []any
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return nil, err
	}
	msgs, err := s.db.Query(`SELECT `+messageColumns+` FROM team_messages WHERE id IN (?`+strings.Repeat(", ?", len(ids)-1)+`) ORDER BY id`, ids...)
	if err != nil {
		return nil, err
	}
	return scanMessages(msgs)
}

// Unclaim queues messages for role again.
func (s *Store) Unclaim(role string, msgs []Message) error {
	for _, m := range msgs {
		if _, err := s.db.Exec(`UPDATE team_deliveries SET delivered_at = 0 WHERE message_id = ? AND role = ?`, m.ID, role); err != nil {
			return err
		}
	}
	return nil
}

// HasPending reports whether messages wait for a role.
func (s *Store) HasPending(teamID, role string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM team_deliveries d JOIN team_messages m ON m.id = d.message_id
		WHERE m.team_id = ? AND d.role = ? AND d.delivered_at = 0`, teamID, role).Scan(&n)
	return n > 0, err
}

// MessageCount returns how many messages each member has sent in a team.
func (s *Store) MessageCount(teamID string) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT from_role, COUNT(*) FROM team_messages WHERE team_id = ? GROUP BY from_role`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var role string
		var n int
		if err := rows.Scan(&role, &n); err != nil {
			return nil, err
		}
		out[role] = n
	}
	return out, rows.Err()
}

// Unread returns, per team, how many channel messages the user hasn't seen
// that someone else wrote.
func (s *Store) Unread() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT m.team_id, COUNT(*) FROM team_messages m
		LEFT JOIN team_reads r ON r.team_id = m.team_id
		WHERE m.id > COALESCE(r.last_id, 0) AND m.from_role <> ? AND m.private = 0
		GROUP BY m.team_id`, FromYou)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// MarkRead records that the user has seen a team's channel up to message id.
func (s *Store) MarkRead(teamID string, id int64) error {
	_, err := s.db.Exec(`INSERT INTO team_reads (team_id, last_id) VALUES (?, ?)
		ON CONFLICT(team_id) DO UPDATE SET last_id = MAX(last_id, excluded.last_id)`, teamID, id)
	return err
}
