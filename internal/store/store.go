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

// Where a session's record came from. Sessions hive makes up from a process
// it sees are "scan" and "pending" (see Synthetic).
const (
	SourceHook     = "hook"     // the agent reported in
	SourceLaunch   = "launch"   // hive started it, knowing its ID ahead of the agent
	SourceImport   = "import"   // read from the tool's own history
	SourceInferred = "inferred" // named as the parent in a child's environment
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
	DeletedAt  int64  `json:"deleted_at,omitempty"` // in the trash since, epoch ms; 0 when not. Read only: see Trash
}

// Synthetic reports whether hive made the session up from a process it saw
// (untracked, or launched and not reported in yet): its ID isn't the tool's.
func (s Session) Synthetic() bool { return s.Source == "scan" || s.Source == "pending" }

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
	// The trash holds the sessions deleted from the tree, which keep their
	// rows until they are restored or deleted for good; purged_sessions
	// remembers what was deleted for good, so a copy a tool keeps somewhere
	// doesn't bring it back. It can run twice: a hive from before it sets the
	// version back to what it knows.
	`CREATE TABLE IF NOT EXISTS trash (
		id TEXT PRIMARY KEY,
		at INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS purged_sessions (
		id TEXT PRIMARY KEY,
		at INTEGER NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS session_usage (
		id                    TEXT PRIMARY KEY,
		model                 TEXT NOT NULL DEFAULT '',
		models                TEXT NOT NULL DEFAULT '{}',
		input_tokens          INTEGER NOT NULL DEFAULT 0,
		output_tokens         INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens     INTEGER NOT NULL DEFAULT 0,
		cache_write_tokens    INTEGER NOT NULL DEFAULT 0,
		cache_write_1h_tokens INTEGER NOT NULL DEFAULT 0,
		reasoning_tokens      INTEGER NOT NULL DEFAULT 0,
		cost_usd              REAL NOT NULL DEFAULT 0,
		cost_source           TEXT NOT NULL DEFAULT '',
		context_tokens        INTEGER NOT NULL DEFAULT 0,
		context_window        INTEGER NOT NULL DEFAULT 0,
		tools                 TEXT NOT NULL DEFAULT '{}',
		skills                TEXT NOT NULL DEFAULT '{}',
		requests              INTEGER NOT NULL DEFAULT 0,
		compactions           INTEGER NOT NULL DEFAULT 0,
		effort                TEXT NOT NULL DEFAULT '',
		api_duration_ms       INTEGER NOT NULL DEFAULT 0,
		lines_added           INTEGER NOT NULL DEFAULT 0,
		lines_removed         INTEGER NOT NULL DEFAULT 0,
		premium_requests      REAL NOT NULL DEFAULT 0,
		partial               INTEGER NOT NULL DEFAULT 0,
		cursor                TEXT NOT NULL DEFAULT '',
		version               INTEGER NOT NULL DEFAULT 0,
		updated_at            INTEGER NOT NULL
	);`,
	// inputs holds the last message hive gave each session that its agent
	// hasn't answered yet, so `hive wait` doesn't take the agent for done
	// before it has started. Like the trash, it can run twice.
	`CREATE TABLE IF NOT EXISTS inputs (
		id TEXT PRIMARY KEY,
		at INTEGER NOT NULL
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
	if version < len(migrations) { // a newer hive may have gone further
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(migrations))); err != nil {
			return err
		}
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}

const columns = `id, tool, native_id, parent_id, title, cwd, kind, status, status_at, pid, pane,
	transcript, last_prompt, created_at, updated_at, source`

// selected is what scan reads: a session's columns, and when it went to the
// trash.
const selected = columns + `, COALESCE((SELECT at FROM trash WHERE trash.id = sessions.id), 0)`

// Whether a session is in the trash, as a condition on sessions.id.
const (
	inTrash    = `sessions.id IN (SELECT id FROM trash)`
	notInTrash = `sessions.id NOT IN (SELECT id FROM trash)`
)

// Upsert inserts a session or merges into the existing row. The first parent,
// folder and kind recorded win; a non-empty title, transcript or prompt
// replaces the old one. Status, process and pane are only set on insert: use
// SetStatus and Attach to change them.
//
// A session in the trash stays there, and one deleted for good stays gone,
// until its agent reports in again or hive starts it: an import or a child
// naming it doesn't bring it back.
func (s *Store) Upsert(x Session) error {
	if x.Status == "" {
		x.Status = StatusUnknown
	}
	if x.ParentID == x.ID {
		x.ParentID = ""
	}
	if x.Source == SourceHook || x.Source == SourceLaunch {
		for _, q := range []string{`DELETE FROM trash WHERE id = ?`, `DELETE FROM purged_sessions WHERE id = ?`} {
			if _, err := s.db.Exec(q, x.ID); err != nil {
				return err
			}
		}
	} else if purged, err := s.purged(x.ID); err != nil || purged {
		return err
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

func (s *Store) purged(id string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM purged_sessions WHERE id = ?`, id).Scan(&n)
	return n > 0, err
}

// PurgedIDs returns the sessions deleted for good.
func (s *Store) PurgedIDs() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT id FROM purged_sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Family returns id and every session under it, oldest first, all in the
// trash or all out of it, as trashed says: a branch in the other state is
// left out, with everything under it.
func (s *Store) Family(id string, trashed bool) ([]Session, error) {
	state := notInTrash
	if trashed {
		state = inTrash
	}
	rows, err := s.db.Query(`WITH RECURSIVE family(id) AS (
			SELECT sessions.id FROM sessions WHERE sessions.id = ? AND `+state+`
			UNION
			SELECT sessions.id FROM sessions JOIN family ON sessions.parent_id = family.id WHERE `+state+`
		)
		SELECT `+selected+` FROM sessions WHERE id IN (SELECT id FROM family)
		ORDER BY created_at, id`, id)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

// Trash moves sessions to the trash as of at.
func (s *Store) Trash(ids []string, at int64) error {
	return s.each(ids, `INSERT OR REPLACE INTO trash (at, id) VALUES (?, ?)`, at)
}

// Restore takes sessions out of the trash.
func (s *Store) Restore(ids []string) error {
	return s.each(ids, `DELETE FROM trash WHERE id = ?`)
}

// each runs q once per id, in one transaction; id is q's last argument.
func (s *Store) each(ids []string, q string, args ...any) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.Exec(q, append(args, id)...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Purge forgets a session for good, with the spawn hints and launch records
// of the commands it ran. A hint naming it as the session a command started
// stays matched, so that command isn't matched to another session.
func (s *Store) Purge(id string, at int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT OR REPLACE INTO purged_sessions (id, at) VALUES (?, ?)`, id, at); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM sessions WHERE id = ?`,
		`DELETE FROM trash WHERE id = ?`,
		`DELETE FROM session_usage WHERE id = ?`,
		`DELETE FROM spawn_hints WHERE parent_id = ?`,
		`DELETE FROM launches WHERE parent_id = ?`,
		`DELETE FROM inputs WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
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

// Get returns one session, in the trash or not.
func (s *Store) Get(id string) (Session, bool, error) {
	rows, err := s.db.Query(`SELECT `+selected+` FROM sessions WHERE id = ?`, id)
	if err != nil {
		return Session{}, false, err
	}
	list, err := scan(rows)
	if err != nil || len(list) == 0 {
		return Session{}, false, err
	}
	return list[0], true, nil
}

// All returns every session that isn't in the trash.
func (s *Store) All() ([]Session, error) {
	rows, err := s.db.Query(`SELECT ` + selected + ` FROM sessions WHERE ` + notInTrash)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

// Trashed returns the sessions in the trash.
func (s *Store) Trashed() ([]Session, error) {
	rows, err := s.db.Query(`SELECT ` + selected + ` FROM sessions WHERE ` + inTrash)
	if err != nil {
		return nil, err
	}
	return scan(rows)
}

// OnPID returns the top-level sessions running as pid, most recently active first.
func (s *Store) OnPID(pid int) ([]Session, error) {
	rows, err := s.db.Query(`SELECT `+selected+` FROM sessions WHERE pid = ? AND kind <> ?
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

// Launches returns the launch records whose agents haven't reported in yet,
// by pane.
func (s *Store) Launches() (map[string]Launch, error) {
	rows, err := s.db.Query(`SELECT pane, tool, parent_id, title, cwd, created_at FROM launches`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Launch{}
	for rows.Next() {
		var l Launch
		if err := rows.Scan(&l.Pane, &l.Tool, &l.ParentID, &l.Title, &l.Cwd, &l.CreatedAt); err != nil {
			return nil, err
		}
		out[l.Pane] = l
	}
	return out, rows.Err()
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

// Inputs are keyed by session ID, or by tmux pane for an agent hive started
// there that hasn't said which session it runs yet; MoveInput hands the
// pane's over once it does.

// SetInput records that hive gave key a message at at, replacing any
// earlier one.
func (s *Store) SetInput(key string, at int64) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO inputs (id, at) VALUES (?, ?)`, key, at)
	return err
}

// Input returns when hive gave key the message its agent hasn't answered
// yet, if there is one.
func (s *Store) Input(key string) (int64, bool, error) {
	var at int64
	err := s.db.QueryRow(`SELECT at FROM inputs WHERE id = ?`, key).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return at, err == nil, err
}

// AnswerInput records that key's agent ended a turn at at, which answers a
// message given at or before then.
func (s *Store) AnswerInput(key string, at int64) error {
	_, err := s.db.Exec(`DELETE FROM inputs WHERE id = ? AND at <= ?`, key, at)
	return err
}

// ClearInput forgets key's unanswered message.
func (s *Store) ClearInput(key string) error {
	_, err := s.db.Exec(`DELETE FROM inputs WHERE id = ?`, key)
	return err
}

// MoveInput hands from's unanswered message to to.
func (s *Store) MoveInput(from, to string) error {
	_, err := s.db.Exec(`UPDATE OR REPLACE inputs SET id = ? WHERE id = ?`, to, from)
	return err
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
	rows, err := s.db.Query(`SELECT `+selected+` FROM sessions
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

// ClaimCandidates returns top-level sessions of tool in cwd, not in the trash,
// with no process recorded, that were active at or after since and did not
// end after it.
func (s *Store) ClaimCandidates(tool, cwd string, since int64) ([]Session, error) {
	rows, err := s.db.Query(`SELECT `+selected+` FROM sessions
		WHERE tool = ? AND cwd = ? AND pid = 0 AND kind <> ? AND updated_at >= ? AND `+notInTrash+`
		AND NOT (status = ? AND status_at > ?)`,
		tool, cwd, KindInternal, since, StatusExited, since)
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
			&x.CreatedAt, &x.UpdatedAt, &x.Source, &x.DeletedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
