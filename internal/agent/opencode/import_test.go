package opencode

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

// fakeDB creates an opencode database with the columns hive reads.
func fakeDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY, parent_id TEXT, directory TEXT NOT NULL,
			title TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL);
		CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL);
		PRAGMA journal_mode = WAL;`)
	if err != nil {
		t.Fatal(err)
	}
	return db, path
}

func TestImport(t *testing.T) {
	db, path := fakeDB(t)
	mustExec(t, db, `INSERT INTO session VALUES
		('ses_top', NULL, '/src/app', 'admin-phase2-backend', 1000, 5000),
		('ses_sub', 'ses_top', '/src/app', 'explore the handlers', 2000, 3000)`)
	mustExec(t, db, `INSERT INTO part VALUES
		('p1', 'm1', 'ses_top', 2500, 2500, '{"type":"tool","tool":"bash","state":{"input":{"command":"claude -p hi","workdir":"/src/app/web"}}}'),
		('p2', 'm1', 'ses_top', 2600, 2600, '{"type":"tool","tool":"read","state":{"input":{"filePath":"x"}}}'),
		('p3', 'm2', 'ses_top', 2700, 2700, '{"type":"tool","tool":"bash","state":{"input":{"command":"go test ./..."}}}')`)

	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{DBPath: path}
	var cmds []agent.ShellCommand
	n, err := a.Import(context.Background(), st, func(c agent.ShellCommand) { cmds = append(cmds, c) })
	if err != nil || n != 2 {
		t.Fatalf("Import = %d, %v", n, err)
	}
	top, _, _ := st.Get("opencode:ses_top")
	if top.Title != "admin-phase2-backend" || top.Cwd != "/src/app" || top.ParentID != "" || top.CreatedAt != 1000 {
		t.Errorf("top = %+v", top)
	}
	sub, _, _ := st.Get("opencode:ses_sub")
	if sub.ParentID != "opencode:ses_top" || sub.Kind != store.KindInternal {
		t.Errorf("sub = %+v", sub)
	}
	want := []agent.ShellCommand{
		{SessionID: "opencode:ses_top", At: 2500, Cwd: "/src/app/web", Command: "claude -p hi"},
		{SessionID: "opencode:ses_top", At: 2700, Cwd: "/src/app", Command: "go test ./..."},
	}
	if len(cmds) != 2 || cmds[0] != want[0] || cmds[1] != want[1] {
		t.Errorf("commands = %+v", cmds)
	}

	// Only sessions updated since the last import are read again.
	mustExec(t, db, `UPDATE session SET time_updated = 9000, title = 'renamed' WHERE id = 'ses_sub'`)
	n, _ = a.Import(context.Background(), st, func(agent.ShellCommand) {})
	if sub, _, _ := st.Get("opencode:ses_sub"); n != 2 || sub.Title != "renamed" {
		// ses_top sits on the cursor boundary and is re-read; that's harmless.
		t.Errorf("second import: n=%d sub=%+v", n, sub)
	}
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.Exec(q); err != nil {
		t.Fatal(err)
	}
}
