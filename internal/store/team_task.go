package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
)

// Statuses of a task on a team's board.
const (
	TaskOpen    = "open"
	TaskClaimed = "claimed"
	TaskDone    = "done"
	TaskBlocked = "blocked"
)

// Task is one numbered piece of a team's work.
type Task struct {
	TeamID    string `json:"team_id"`
	N         int    `json:"n"` // #1, #2 … per team
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	Status    string `json:"status"`
	Owner     string `json:"owner,omitempty"` // a role
	After     []int  `json:"after"`           // the tasks it waits for
	Note      string `json:"note,omitempty"`  // what done changed, or why it is blocked
	CreatedBy string `json:"created_by,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// Closed reports whether nobody works on the task: it is done or blocked.
func (t Task) Closed() bool { return t.Status == TaskDone || t.Status == TaskBlocked }

// The task board. Like the trash, it can run twice.
const teamTasksSchema = `CREATE TABLE IF NOT EXISTS team_tasks (
		team_id    TEXT NOT NULL,
		n          INTEGER NOT NULL,
		title      TEXT NOT NULL,
		body       TEXT NOT NULL DEFAULT '',
		status     TEXT NOT NULL DEFAULT 'open',
		owner      TEXT NOT NULL DEFAULT '',
		after      TEXT NOT NULL DEFAULT '[]',
		note       TEXT NOT NULL DEFAULT '',
		created_by TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (team_id, n)
	);`

const taskColumns = `team_id, n, title, body, status, owner, after, note, created_by, created_at, updated_at`

func scanTasks(rows *sql.Rows) ([]Task, error) {
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		var after string
		if err := rows.Scan(&t.TeamID, &t.N, &t.Title, &t.Body, &t.Status, &t.Owner, &after, &t.Note,
			&t.CreatedBy, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(after), &t.After); err != nil {
			return nil, fmt.Errorf("task #%d: %w", t.N, err)
		}
		if t.After == nil {
			t.After = []int{}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AddTask puts a task on a team's board under the next number, and returns
// it as stored.
func (s *Store) AddTask(t Task) (Task, error) {
	if t.Status == "" {
		t.Status = TaskOpen
	}
	if t.After == nil {
		t.After = []int{}
	}
	after, err := json.Marshal(t.After)
	if err != nil {
		return Task{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	// The number is taken inside the insert, so two members adding at once
	// can't get the same one.
	if err := tx.QueryRow(`INSERT INTO team_tasks (`+taskColumns+`)
		VALUES (?, (SELECT COALESCE(MAX(n), 0) + 1 FROM team_tasks WHERE team_id = ?), ?, ?, ?, ?, ?, ?, ?, ?, ?)
		RETURNING n`, t.TeamID, t.TeamID, t.Title, t.Body, t.Status, t.Owner, string(after), t.Note, t.CreatedBy,
		t.CreatedAt, t.UpdatedAt).Scan(&t.N); err != nil {
		return Task{}, err
	}
	if _, err := tx.Exec(`UPDATE teams SET updated_at = MAX(updated_at, ?) WHERE id = ?`, t.UpdatedAt, t.TeamID); err != nil {
		return Task{}, err
	}
	return t, tx.Commit()
}

// Tasks returns a team's tasks by number.
func (s *Store) Tasks(teamID string) ([]Task, error) {
	rows, err := s.db.Query(`SELECT `+taskColumns+` FROM team_tasks WHERE team_id = ? ORDER BY n`, teamID)
	if err != nil {
		return nil, err
	}
	return scanTasks(rows)
}

// AllTasks returns the tasks of every team, by team and number.
func (s *Store) AllTasks() ([]Task, error) {
	rows, err := s.db.Query(`SELECT ` + taskColumns + ` FROM team_tasks ORDER BY team_id, n`)
	if err != nil {
		return nil, err
	}
	return scanTasks(rows)
}

// Task returns task n of a team.
func (s *Store) Task(teamID string, n int) (Task, error) {
	rows, err := s.db.Query(`SELECT `+taskColumns+` FROM team_tasks WHERE team_id = ? AND n = ?`, teamID, n)
	if err != nil {
		return Task{}, err
	}
	tasks, err := scanTasks(rows)
	if err != nil {
		return Task{}, err
	}
	if len(tasks) == 0 {
		return Task{}, fmt.Errorf("the board has no #%d; `hive team task ls` lists the tasks", n)
	}
	return tasks[0], nil
}

// UpdateTask moves a task from what was read (old) to its new status, owner
// and note, and reports whether it did: it doesn't when someone else moved
// the task in between, as two members claiming it at once would.
func (s *Store) UpdateTask(old, t Task) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE team_tasks SET status = ?, owner = ?, note = ?, updated_at = ?
		WHERE team_id = ? AND n = ? AND status = ? AND owner = ?`,
		t.Status, t.Owner, t.Note, t.UpdatedAt, old.TeamID, old.N, old.Status, old.Owner)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	if _, err := tx.Exec(`UPDATE teams SET updated_at = MAX(updated_at, ?) WHERE id = ?`, t.UpdatedAt, old.TeamID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// EditTask moves a task's title, body, status, owner, note and the tasks it
// waits for from what was read (old) to what it now is, and reports whether
// it did: it doesn't when someone else moved the task in between.
func (s *Store) EditTask(old, t Task) (bool, error) {
	if t.After == nil {
		t.After = []int{}
	}
	if old.After == nil {
		old.After = []int{}
	}
	after, err := json.Marshal(t.After)
	if err != nil {
		return false, err
	}
	was, err := json.Marshal(old.After)
	if err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE team_tasks SET title = ?, body = ?, status = ?, owner = ?, after = ?, note = ?, updated_at = ?
		WHERE team_id = ? AND n = ? AND status = ? AND owner = ? AND title = ? AND body = ? AND after = ?`,
		t.Title, t.Body, t.Status, t.Owner, string(after), t.Note, t.UpdatedAt,
		old.TeamID, old.N, old.Status, old.Owner, old.Title, old.Body, string(was))
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	if _, err := tx.Exec(`UPDATE teams SET updated_at = MAX(updated_at, ?) WHERE id = ?`, t.UpdatedAt, old.TeamID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// sameTask reports whether now is still the task old was read as.
func sameTask(now, old Task) bool {
	return now.Status == old.Status && now.Owner == old.Owner && now.Title == old.Title &&
		now.Body == old.Body && slices.Equal(now.After, old.After)
}

// UpdateTaskChecked moves a task from what was read (old) to t, and lets check
// see the team's whole board as it is inside the same transaction, with t in
// place, so that a check over the board and the write can't race another
// member's edit. It reports false when the task changed since old was read,
// and writes nothing when check refuses.
func (s *Store) UpdateTaskChecked(old, t Task, check func(tasks []Task) error) (bool, error) {
	if t.After == nil {
		t.After = []int{}
	}
	if old.After == nil {
		old.After = []int{}
	}
	after, err := json.Marshal(t.After)
	if err != nil {
		return false, err
	}
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	// The read must be under the write lock: two edits that each check the
	// board before either writes can leave a ring otherwise.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return false, err
	}
	committed := false
	defer func() {
		if !committed {
			conn.ExecContext(ctx, "ROLLBACK")
		}
	}()
	rows, err := conn.QueryContext(ctx, `SELECT `+taskColumns+` FROM team_tasks WHERE team_id = ? ORDER BY n`, old.TeamID)
	if err != nil {
		return false, err
	}
	tasks, err := scanTasks(rows)
	if err != nil {
		return false, err
	}
	i := slices.IndexFunc(tasks, func(x Task) bool { return x.N == old.N })
	if i < 0 || !sameTask(tasks[i], old) {
		return false, nil
	}
	tasks[i] = t
	if check != nil {
		if err := check(tasks); err != nil {
			return false, err
		}
	}
	res, err := conn.ExecContext(ctx, `UPDATE team_tasks SET title = ?, body = ?, status = ?, owner = ?, after = ?, note = ?, updated_at = ?
		WHERE team_id = ? AND n = ?`,
		t.Title, t.Body, t.Status, t.Owner, string(after), t.Note, t.UpdatedAt, old.TeamID, old.N)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, nil
	}
	if _, err := conn.ExecContext(ctx, `UPDATE teams SET updated_at = MAX(updated_at, ?) WHERE id = ?`, t.UpdatedAt, old.TeamID); err != nil {
		return false, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return false, err
	}
	committed = true
	return true, nil
}

// MessagesAfter returns a team's messages after id, oldest first, private
// ones too.
func (s *Store) MessagesAfter(teamID string, id int64) ([]Message, error) {
	rows, err := s.db.Query(`SELECT `+messageColumns+` FROM team_messages WHERE team_id = ? AND id > ? ORDER BY id`, teamID, id)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// LastMessageID is the newest message in a team's channel, or 0.
func (s *Store) LastMessageID(teamID string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM team_messages WHERE team_id = ?`, teamID).Scan(&id)
	return id, err
}

// ClaimMessages marks some of the messages queued for a role delivered, and
// returns those it marked: one a delivery took first is left out.
func (s *Store) ClaimMessages(role string, msgs []Message, at int64) ([]Message, error) {
	var out []Message
	for _, m := range msgs {
		res, err := s.db.Exec(`UPDATE team_deliveries SET delivered_at = ? WHERE message_id = ? AND role = ? AND delivered_at = 0`,
			at, m.ID, role)
		if err != nil {
			return out, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			out = append(out, m)
		}
	}
	return out, nil
}
