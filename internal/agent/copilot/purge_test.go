package copilot

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/sadrishehu/hive/internal/agent/agenttest"
	"github.com/sadrishehu/hive/internal/store"
)

func TestPurgeDeletesStateAndIndex(t *testing.T) {
	home := t.TempDir()
	const id, other = "08b29aaf-7c28-4459-9b43-6ca224ce317f", "1d2a66f0-a8c8-4988-87f2-944cc09bb315"
	agenttest.Files(t, home,
		"session-state/"+id+"/events.jsonl", "session-state/"+id+"/files/a.go",
		"session-state/"+other+"/events.jsonl")
	dbPath := filepath.Join(home, "session-store.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, cwd TEXT, summary TEXT)`,
		`CREATE TABLE turns (id INTEGER PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id), user_message TEXT)`,
		`CREATE TABLE dynamic_context_items (repository TEXT, name TEXT)`,
		`CREATE VIRTUAL TABLE search_index USING fts5(content, session_id UNINDEXED)`,
		`INSERT INTO sessions VALUES ('` + id + `', '/src', 'mine'), ('` + other + `', '/src', 'theirs')`,
		`INSERT INTO turns (session_id, user_message) VALUES ('` + id + `', 'fix it'), ('` + other + `', 'ship it')`,
		`INSERT INTO dynamic_context_items VALUES ('repo', 'n')`,
		`INSERT INTO search_index VALUES ('fix the parser', '` + id + `'), ('ship the parser', '` + other + `')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	a := &Adapter{HomeDir: home}
	if err := a.Purge(context.Background(), store.Session{NativeID: id}); err != nil {
		t.Fatal(err)
	}
	if left := agenttest.Exists(home, "session-state/"+id, "session-state/"+other+"/events.jsonl"); len(left) != 1 || left[0] == "session-state/"+id {
		t.Errorf("session-state left: %v, want only the other session's", left)
	}
	count := func(q string) int {
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM sessions`); n != 1 {
		t.Errorf("%d sessions left, want 1", n)
	}
	if n := count(`SELECT COUNT(*) FROM turns WHERE session_id = '` + id + `'`); n != 0 {
		t.Errorf("%d of its turns left", n)
	}
	if n := count(`SELECT COUNT(*) FROM search_index WHERE search_index MATCH 'parser'`); n != 1 {
		t.Errorf("search finds %d sessions, want only the other one", n)
	}
	if n := count(`SELECT COUNT(*) FROM dynamic_context_items`); n != 1 {
		t.Error("deleted from a table that isn't about sessions")
	}
	if err := a.Purge(context.Background(), store.Session{NativeID: id}); err != nil {
		t.Errorf("purging it again: %v", err)
	}
	if err := a.Purge(context.Background(), store.Session{NativeID: "../" + other}); err == nil {
		t.Error("purged an ID that isn't one")
	}
}
