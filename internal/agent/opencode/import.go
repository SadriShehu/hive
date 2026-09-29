package opencode

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"strconv"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

// cursorKey stores the newest session update already imported.
const cursorKey = "opencode:cursor"

// Import reads sessions updated since the last import from opencode's
// database, opened read-only, along with the bash commands they ran.
func (a *Adapter) Import(ctx context.Context, st *store.Store, found func(agent.ShellCommand)) (int, error) {
	path := a.dbPath()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return 0, nil // opencode never ran here
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var cursor int64
	if v, ok, err := st.ImportState(cursorKey); err != nil {
		return 0, err
	} else if ok {
		cursor, _ = strconv.ParseInt(v, 10, 64)
	}

	type row struct {
		id, parent, dir, title string
		created, updated       int64
	}
	rows, err := db.QueryContext(ctx, `SELECT id, COALESCE(parent_id, ''), directory, title, time_created, time_updated
		FROM session WHERE time_updated >= ? ORDER BY time_updated`, cursor)
	if err != nil {
		return 0, err
	}
	var changed []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.parent, &r.dir, &r.title, &r.created, &r.updated); err != nil {
			rows.Close()
			return 0, err
		}
		changed = append(changed, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	next := cursor
	for _, r := range changed {
		s := store.Session{
			ID: store.ID(name, r.id), Tool: name, NativeID: r.id, Title: r.title, Cwd: r.dir,
			Status: store.StatusExited, CreatedAt: r.created, UpdatedAt: r.updated, Source: store.SourceImport,
		}
		if r.parent != "" {
			s.ParentID = store.ID(name, r.parent)
			s.Kind = store.KindInternal
		}
		if err := st.Upsert(s); err != nil {
			return 0, err
		}
		if err := bashCommands(ctx, db, s, cursor, found); err != nil {
			return 0, err
		}
		next = max(next, r.updated)
	}
	return len(changed), st.SetImportState(cursorKey, strconv.FormatInt(next, 10))
}

// bashCommands passes the bash commands s ran since cursor to found.
func bashCommands(ctx context.Context, db *sql.DB, s store.Session, cursor int64, found func(agent.ShellCommand)) error {
	rows, err := db.QueryContext(ctx, `SELECT time_created,
			COALESCE(json_extract(data, '$.state.input.command'), ''),
			COALESCE(json_extract(data, '$.state.input.workdir'), '')
		FROM part WHERE session_id = ? AND time_created >= ? AND data LIKE '%"tool":"bash"%'`,
		s.NativeID, cursor)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var at int64
		var command, workdir string
		if err := rows.Scan(&at, &command, &workdir); err != nil {
			return err
		}
		if workdir == "" {
			workdir = s.Cwd
		}
		found(agent.ShellCommand{SessionID: s.ID, At: at, Cwd: workdir, Command: command})
	}
	return rows.Err()
}
