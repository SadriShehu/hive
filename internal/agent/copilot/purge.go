package copilot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

// Purge deletes a session as Copilot CLI does, removing its folder under
// session-state, and also its rows in session-store.db, the index behind
// Copilot's session search, which Copilot's own delete leaves behind.
func (a *Adapter) Purge(ctx context.Context, s store.Session) error {
	if !agent.SafeID(s.NativeID) {
		return fmt.Errorf("unexpected session ID %q", s.NativeID)
	}
	if err := os.RemoveAll(filepath.Join(a.sessionStateDir(), s.NativeID)); err != nil {
		return err
	}
	return a.unindex(ctx, s.NativeID)
}

// unindex deletes a session's rows from session-store.db: its own row, and
// every row of any table with a session_id column.
func (a *Adapter) unindex(ctx context.Context, id string) error {
	path := a.dbPath()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	tables, err := sessionTables(ctx, db)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM "`+table+`" WHERE session_id = ?`, id); err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// sessionTables lists the tables with a session_id column. The search
// index's own storage tables have none; deleting from the index clears them.
func sessionTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT m.name FROM sqlite_master m, pragma_table_info(m.name) c
		WHERE m.type = 'table' AND c.name = 'session_id' ORDER BY m.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}
