// Package store persists sessions in the SQLite database shared by every hive
// process: hooks from many agents write to it at once, and the TUI reads it.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Kinds of session.
const (
	KindInteractive = "interactive"
	KindHeadless    = "headless"
	KindInternal    = "internal" // a tool's own subagent, living inside its parent's process
)

// Statuses of a session.
const (
	StatusUnknown   = "unknown"
	StatusWorking   = "working"
	StatusAttention = "attention" // waiting on the user: a permission prompt or a question
	StatusIdle      = "idle"
	StatusExited    = "exited"
)

// Session is one agent session from any tool.
type Session struct {
	ID         string `json:"id"` // "<tool>:<native_id>"
	Tool       string `json:"tool"`
	NativeID   string `json:"native_id"`
	ParentID   string `json:"parent_id,omitempty"`
	Title      string `json:"title,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Status     string `json:"status"`
	StatusAt   int64  `json:"status_at"`
	PID        int    `json:"pid,omitempty"`
	Pane       string `json:"pane,omitempty"`
	Transcript string `json:"transcript,omitempty"`
	LastPrompt string `json:"last_prompt,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	Source     string `json:"source,omitempty"`
}

// ID builds a session ID from a tool name and the tool's own session ID.
func ID(tool, nativeID string) string { return tool + ":" + nativeID }

// Live reports whether the session is running right now. A tool's own
// subagent has no process of its own, so its status decides.
func (s Session) Live() bool {
	if s.Kind == KindInternal {
		return s.Status != StatusExited && s.Status != StatusUnknown
	}
	return s.PID > 0
}

// Launch is what hive asked for when it started an agent in a tmux pane,
// kept until the agent in that pane reports in.
type Launch struct {
	Pane      string
	Tool      string
	ParentID  string
	Title     string
	Cwd       string
	CreatedAt int64
}

// Store is the session database.
type Store struct {
	db *sql.DB
}

var migrations = []string{
	`CREATE TABLE sessions (
		id          TEXT PRIMARY KEY,
		tool        TEXT NOT NULL,
		native_id   TEXT NOT NULL,
		parent_id   TEXT NOT NULL DEFAULT '',
		title       TEXT NOT NULL DEFAULT '',
		cwd         TEXT NOT NULL DEFAULT '',
		kind        TEXT NOT NULL DEFAULT '',
		status      TEXT NOT NULL DEFAULT 'unknown',
		status_at   INTEGER NOT NULL DEFAULT 0,
		pid         INTEGER NOT NULL DEFAULT 0,
		pane        TEXT NOT NULL DEFAULT '',
		transcript  TEXT NOT NULL DEFAULT '',
		last_prompt TEXT NOT NULL DEFAULT '',
		created_at  INTEGER NOT NULL,
		updated_at  INTEGER NOT NULL,
		source      TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX sessions_parent ON sessions(parent_id);
	CREATE INDEX sessions_pid ON sessions(pid) WHERE pid > 0;
	CREATE TABLE launches (
		pane       TEXT PRIMARY KEY,
		tool       TEXT NOT NULL,
		parent_id  TEXT NOT NULL DEFAULT '',
		title      TEXT NOT NULL DEFAULT '',
		cwd        TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL
	);`,
	`CREATE TABLE spawn_hints (
		parent_id TEXT NOT NULL,
		at        INTEGER NOT NULL,
		tool      TEXT NOT NULL,
		title     TEXT NOT NULL DEFAULT '',
		native_id TEXT NOT NULL DEFAULT '',
		cwd       TEXT NOT NULL DEFAULT '',
		headless  INTEGER NOT NULL DEFAULT 0,
		child_id  TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (parent_id, at, tool, title, native_id)
	);
	CREATE INDEX spawn_hints_open ON spawn_hints(child_id);
	CREATE TABLE import_state (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);`,
}

// Open opens (creating if needed) the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	// Switching a fresh database to WAL needs a moment alone with the file,
	// and SQLite reports a clash as busy at once instead of waiting. Hooks
	// from several agents can open a fresh database together, so retry.
	err = retryBusy(func() error {
		if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
			return err
		}
		return s.migrate()
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return s, nil
}

func retryBusy(f func() error) error {
	var err error
	for range 100 {
		if err = f(); err == nil || !isBusy(err) {
			return err
		}
		time.Sleep(time.Duration(10+rand.IntN(40)) * time.Millisecond)
	}
	return err
}

func isBusy(err error) bool {
	e, ok := errors.AsType[interface {
		error
		Code() int
	}](err)
	return ok && (e.Code()&0xff == 5 || e.Code()&0xff == 6) // SQLITE_BUSY, SQLITE_LOCKED
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// migrate brings the schema up to date. Several hooks can open a fresh
// database at once, so the version check runs under a write lock.
func (s *Store) migrate() (err error) {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			conn.ExecContext(ctx, "ROLLBACK")
		}
	}()
	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	for i := version; i < len(migrations); i++ {
		if _, err := conn.ExecContext(ctx, migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(migrations))); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

const columns = `id, tool, native_id, parent_id, title, cwd, kind, status, status_at, pid, pane,
	transcript, last_prompt, created_at, updated_at, source`

// Upsert inserts a session or merges into the existing row. The first parent,
// folder and kind recorded win; a non-empty title, transcript or prompt
// replaces the old one. Status, process and pane are only set on insert: use
// SetStatus and Attach to change them.
func (s *Store) Upsert(x Session) error {
	if x.Status == "" {
		x.Status = StatusUnknown
	}
	if x.ParentID == x.ID {
		x.ParentID = ""
	}
	_, err := s.db.Exec(`INSERT INTO sessions (`+columns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			parent_id   = CASE WHEN sessions.parent_id = '' THEN excluded.parent_id ELSE sessions.parent_id END,
			title       = CASE WHEN excluded.title <> '' THEN excluded.title ELSE sessions.title END,
			cwd         = CASE WHEN sessions.cwd = '' THEN excluded.cwd ELSE sessions.cwd END,
			kind        = CASE WHEN sessions.kind = '' THEN excluded.kind ELSE sessions.kind END,
			transcript  = CASE WHEN excluded.transcript <> '' THEN excluded.transcript ELSE sessions.transcript END,
			last_prompt = CASE WHEN excluded.last_prompt <> '' THEN excluded.last_prompt ELSE sessions.last_prompt END,
			created_at  = MIN(sessions.created_at, excluded.created_at),
			updated_at  = MAX(sessions.updated_at, excluded.updated_at)`,
		x.ID, x.Tool, x.NativeID, x.ParentID, x.Title, x.Cwd, x.Kind, x.Status, x.StatusAt, x.PID, x.Pane,
		x.Transcript, x.LastPrompt, x.CreatedAt, x.UpdatedAt, x.Source)
	return err
}

// Attach records the process and tmux pane a session runs in. An empty kind
// keeps the current one.
func (s *Store) Attach(id string, pid int, pane, kind string) error {
	_, err := s.db.Exec(`UPDATE sessions SET pid = ?, pane = ?,
		kind = CASE WHEN ? <> '' THEN ? ELSE kind END WHERE id = ?`, pid, pane, kind, kind, id)
	return err
}

// SetStatus records status as of at, unless a newer status is already
// recorded; events from one agent can arrive out of order. Exiting also
// forgets the process and pane. Activity time (updated_at) is the caller's.
func (s *Store) SetStatus(id, status string, at int64) error {
	q := `UPDATE sessions SET status = ?, status_at = ?`
	if status == StatusExited {
		q += `, pid = 0, pane = ''`
	}
	_, err := s.db.Exec(q+` WHERE id = ? AND status_at <= ?`, status, at, id, at)
	return err
}

// MarkGone records that a session's process no longer exists, whatever
// status was recorded before.
func (s *Store) MarkGone(id string, at int64) error {
	_, err := s.db.Exec(`UPDATE sessions SET status = ?, status_at = MAX(status_at, ?),
		updated_at = MAX(updated_at, ?), pid = 0, pane = '' WHERE id = ?`, StatusExited, at, at, id)
	return err
}

// EndOthersOnPID marks every other top-level session running as pid exited:
// the process moved on to a new session (/clear in Claude, /new in opencode).
func (s *Store) EndOthersOnPID(pid int, keep string, at int64) error {
	_, err := s.db.Exec(`UPDATE sessions SET status = ?, status_at = MAX(status_at, ?), pid = 0, pane = ''
		WHERE pid = ? AND id <> ? AND kind <> ?`, StatusExited, at, pid, keep, KindInternal)
	return err
}

// Get returns one session.
func (s *Store) Get(id string) (Session, bool, error) {
	rows, err := s.db.Query(`SELECT `+columns+` FROM sessions WHERE id = ?`, id)
	if err != nil {
		return Session{}, false, err
	}
	list, err := scan(rows)
	if err != nil || len(list) == 0 {
		return Session{}, false, err
	}
	return list[0], true, nil
}

// All returns every session.
func (s *Store) All() ([]Session, error) {
	rows, err := s.db.Query(`SELECT ` + columns + ` FROM sessions`)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

// OnPID returns the top-level sessions running as pid, most recently active first.
func (s *Store) OnPID(pid int) ([]Session, error) {
	rows, err := s.db.Query(`SELECT `+columns+` FROM sessions WHERE pid = ? AND kind <> ?
		ORDER BY updated_at DESC`, pid, KindInternal)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

// PutLaunch remembers what hive asked for when it started an agent in pane.
func (s *Store) PutLaunch(l Launch) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO launches (pane, tool, parent_id, title, cwd, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, l.Pane, l.Tool, l.ParentID, l.Title, l.Cwd, l.CreatedAt)
	return err
}

// TakeLaunch returns and forgets the launch record for pane, if one was made
// after since. Older records belong to an agent that never reported in.
func (s *Store) TakeLaunch(pane string, since int64) (Launch, bool, error) {
	l := Launch{Pane: pane}
	err := s.db.QueryRow(`DELETE FROM launches WHERE pane = ?
		RETURNING tool, parent_id, title, cwd, created_at`, pane).
		Scan(&l.Tool, &l.ParentID, &l.Title, &l.Cwd, &l.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Launch{}, false, nil
	}
	if err != nil {
		return Launch{}, false, err
	}
	return l, l.CreatedAt >= since, nil
}

// SetParent links id to parent, unless id already has a parent.
func (s *Store) SetParent(id, parent string) (bool, error) {
	res, err := s.db.Exec(`UPDATE sessions SET parent_id = ? WHERE id = ? AND parent_id = ''`, parent, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Hint records that a session ran a command starting another agent. Sync
// matches hints to sessions to link spawns from before hive was installed.
type Hint struct {
	ParentID string
	At       int64 // when the command ran, epoch ms
	Tool     string
	Title    string // the title the command gave the new session
	NativeID string // the session the command named, if it did
	Cwd      string
	Headless bool   // the command was a non-interactive run
	ChildID  string // the matched session; "" while open, HintGaveUp when never matched
}

// HintGaveUp marks a hint that never matched a session.
const HintGaveUp = "-"

// PutHint records a hint; recording the same one twice is a no-op.
func (s *Store) PutHint(h Hint) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO spawn_hints (parent_id, at, tool, title, native_id, cwd, headless)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, h.ParentID, h.At, h.Tool, h.Title, h.NativeID, h.Cwd, h.Headless)
	return err
}

// OpenHints returns the hints not yet matched to a session.
func (s *Store) OpenHints() ([]Hint, error) {
	rows, err := s.db.Query(`SELECT parent_id, at, tool, title, native_id, cwd, headless FROM spawn_hints
		WHERE child_id = '' ORDER BY at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hint
	for rows.Next() {
		var h Hint
		if err := rows.Scan(&h.ParentID, &h.At, &h.Tool, &h.Title, &h.NativeID, &h.Cwd, &h.Headless); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// CloseHint records the session a hint matched, or HintGaveUp.
func (s *Store) CloseHint(h Hint, childID string) error {
	_, err := s.db.Exec(`UPDATE spawn_hints SET child_id = ?
		WHERE parent_id = ? AND at = ? AND tool = ? AND title = ? AND native_id = ?`,
		childID, h.ParentID, h.At, h.Tool, h.Title, h.NativeID)
	return err
}

// SpawnCandidates returns top-level sessions of tool created between from and
// to, oldest first, that could be parent's child: they have no other parent
// and no other command claimed to start them. (A command that continues a
// session by ID doesn't claim it.)
func (s *Store) SpawnCandidates(tool, parent string, from, to int64) ([]Session, error) {
	rows, err := s.db.Query(`SELECT `+columns+` FROM sessions
		WHERE tool = ? AND kind <> ? AND id <> ? AND (parent_id = '' OR parent_id = ?)
		AND created_at BETWEEN ? AND ?
		AND id NOT IN (SELECT child_id FROM spawn_hints WHERE native_id = '')
		ORDER BY created_at`, tool, KindInternal, parent, parent, from, to)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

// SetKindIfUnknown records a session's kind when nothing recorded one yet.
func (s *Store) SetKindIfUnknown(id, kind string) error {
	_, err := s.db.Exec(`UPDATE sessions SET kind = ? WHERE id = ? AND kind = ''`, kind, id)
	return err
}

// ClaimCandidates returns top-level sessions of tool in cwd with no process
// recorded that were active at or after since.
func (s *Store) ClaimCandidates(tool, cwd string, since int64) ([]Session, error) {
	rows, err := s.db.Query(`SELECT `+columns+` FROM sessions
		WHERE tool = ? AND cwd = ? AND pid = 0 AND kind <> ? AND updated_at >= ?`,
		tool, cwd, KindInternal, since)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

// ImportState returns what an importer stored under key.
func (s *Store) ImportState(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM import_state WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetImportState stores an importer's progress under key.
func (s *Store) SetImportState(key, value string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO import_state (key, value) VALUES (?, ?)`, key, value)
	return err
}

// ResetImports forgets all import progress, so the next sync reads everything.
func (s *Store) ResetImports() error {
	_, err := s.db.Exec(`DELETE FROM import_state`)
	return err
}

func scan(rows *sql.Rows) ([]Session, error) {
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var x Session
		if err := rows.Scan(&x.ID, &x.Tool, &x.NativeID, &x.ParentID, &x.Title, &x.Cwd, &x.Kind,
			&x.Status, &x.StatusAt, &x.PID, &x.Pane, &x.Transcript, &x.LastPrompt,
			&x.CreatedAt, &x.UpdatedAt, &x.Source); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
