package copilot

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

func TestImport(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "session-state")
	id := "9b7a1d18-1b42-4f9f-a64a-5a785a04d4df"
	sessionDir := filepath.Join(stateDir, id)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	events := `{"id":"e1","type":"session.start","timestamp":"2026-09-28T08:00:00.000Z","data":{"sessionId":"` + id + `","startTime":"2026-09-28T08:00:00.000Z","context":{"cwd":"/src/copilot"}}}
{"id":"e2","type":"user.message","timestamp":"2026-09-28T08:00:01.000Z","data":{"content":"implement the feature"}}
{"id":"e3","type":"tool.execution_start","timestamp":"2026-09-28T08:00:02.000Z","data":{"toolName":"bash","arguments":{"command":"claude -p review","workdir":"/src/copilot"}}}
{"id":"e4","type":"assistant.message","timestamp":"2026-09-28T08:00:03.000Z","data":{"content":"Done."}}
`
	if err := os.WriteFile(filepath.Join(sessionDir, "events.jsonl"), []byte(events), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "workspace.yaml"), []byte(
		"id: "+id+"\ncwd: /src/copilot\nsummary: Imported title\ncreated_at: 2026-09-28T08:00:00.000Z\nupdated_at: 2026-09-28T08:00:03.000Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "session-store.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY, cwd TEXT, summary TEXT, created_at TEXT, updated_at TEXT);
		CREATE TABLE turns (
		id INTEGER PRIMARY KEY, session_id TEXT, turn_index INTEGER,
		user_message TEXT, assistant_response TEXT, timestamp TEXT);
		INSERT INTO sessions VALUES (
		'` + id + `', '/src/copilot', 'Fixture session',
		'2026-09-28T08:00:00.000Z', '2026-09-28T08:00:03.000Z');
		INSERT INTO turns VALUES (1, '` + id + `', 1, 'implement the feature', 'Done.',
		'2026-09-28T08:00:01.000Z');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{SessionStateDir: stateDir, DBPath: dbPath}
	var commands []agent.ShellCommand
	n, err := a.Import(context.Background(), st, func(c agent.ShellCommand) { commands = append(commands, c) })
	if err != nil || n != 1 {
		t.Fatalf("Import = %d, %v; want one session", n, err)
	}
	s, ok, err := st.Get("copilot:" + id)
	if err != nil || !ok {
		t.Fatalf("session missing: %v", err)
	}
	wantCreated := timestamp("2026-09-28T08:00:00.000Z")
	wantUpdated := timestamp("2026-09-28T08:00:03.000Z")
	if s.Title != "Fixture session" || s.LastPrompt != "implement the feature" || s.Cwd != "/src/copilot" ||
		s.CreatedAt != wantCreated || s.UpdatedAt != wantUpdated || s.Transcript != filepath.Join(sessionDir, "events.jsonl") {
		t.Errorf("session = %+v", s)
	}
	wantCommands := []agent.ShellCommand{{
		SessionID: "copilot:" + id, At: timestamp("2026-09-28T08:00:02.000Z"),
		Cwd: "/src/copilot", Command: "claude -p review",
	}}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Errorf("commands = %+v, want %+v", commands, wantCommands)
	}
	if n, err := a.Import(context.Background(), st, func(agent.ShellCommand) {}); err != nil || n != 0 {
		t.Errorf("unchanged import = %d, %v; want no sessions", n, err)
	}
	lines, err := a.Tail(s, 3)
	if err != nil || len(lines) != 3 || lines[0].Role != "user" ||
		lines[1].Role != "tool" || lines[2] != (agent.Line{Role: "assistant", Text: "Done."}) {
		t.Errorf("Tail = %+v, %v", lines, err)
	}
	if err := a.CheckResume(s); err != nil {
		t.Errorf("saved session can't resume: %v", err)
	}
	if err := a.CheckResume(store.Session{NativeID: "missing"}); err == nil {
		t.Error("missing session state was allowed to resume")
	}
}
