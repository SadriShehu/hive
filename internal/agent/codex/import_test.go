package codex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tidwall/gjson"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

const (
	parentID = "01a0ebee-33d6-7860-ab39-b19bc0a7c33c"
	childID  = "02b1fcff-44e7-4971-bc4a-c2acd1b8d44d"
	emptyID  = "03c2fd00-55f8-4a82-8d5b-d3bde2c9e55e"
	dbOnlyID = "04d3fe11-6609-4b93-9e6c-e4cef3d0f66f"
)

const parentRollout = `{"timestamp":"2026-09-29T06:52:24.919Z","ordinal":0,"type":"session_meta","payload":{"session_id":"` + parentID + `","id":"` + parentID + `","timestamp":"2026-09-29T06:50:51.738Z","cwd":"/src/codex","originator":"codex-tui","cli_version":"0.158.0","source":"cli","thread_source":"user","base_instructions":{"text":"You are Codex."}}}
{"timestamp":"2026-09-29T06:52:24.937Z","ordinal":1,"type":"response_item","payload":{"type":"message","id":"msg_1","role":"developer","content":[{"type":"input_text","text":"<model_switch>ignore</model_switch>"}]}}
{"timestamp":"2026-09-29T06:52:24.937Z","ordinal":2,"type":"response_item","payload":{"type":"message","id":"msg_2","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/src/codex</cwd>\n</environment_context>"}]}}
{"timestamp":"2026-09-29T06:52:24.939Z","ordinal":3,"type":"turn_context","payload":{"turn_id":"t1","cwd":"/src/codex","approval_policy":"on-request"}}
{"timestamp":"2026-09-29T06:52:24.948Z","ordinal":4,"type":"response_item","payload":{"type":"message","id":"msg_3","role":"user","content":[{"type":"input_text","text":"implement the feature"}]}}
{"timestamp":"2026-09-29T06:52:24.949Z","ordinal":5,"type":"event_msg","payload":{"type":"item_completed","thread_id":"` + parentID + `","turn_id":"t1","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"implement the feature"}]}}}
{"timestamp":"2026-09-29T06:52:27.029Z","ordinal":6,"type":"response_item","payload":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"xxx"}}
{"timestamp":"2026-09-29T06:52:27.518Z","ordinal":7,"type":"response_item","payload":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"shell","arguments":"{\"command\":[\"bash\",\"-lc\",\"claude -p review\"],\"workdir\":\"/src/codex\"}"}}
{"timestamp":"2026-09-29T06:52:27.600Z","ordinal":8,"type":"event_msg","payload":{"type":"exec_command_begin","call_id":"call_1","command":["bash","-lc","claude -p review"],"cwd":"/src/codex"}}
{"timestamp":"2026-09-29T06:52:28.909Z","ordinal":9,"type":"event_msg","payload":{"type":"item_completed","thread_id":"` + parentID + `","turn_id":"t1","item":{"type":"CommandExecution","id":"exec-1","command":["/bin/zsh","-lc","opencode run --title impl-be 'go'"],"cwd":"file:///src/codex/backend","status":"completed","exit_code":0}}}
{"timestamp":"2026-09-29T06:52:30.702Z","ordinal":10,"type":"response_item","payload":{"type":"message","id":"msg_4","role":"assistant","content":[{"type":"output_text","text":"Done."}],"phase":"final_answer"}}
{"timestamp":"2026-09-29T06:52:30.713Z","ordinal":11,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":"Done."}}
`

const childRollout = `{"timestamp":"2026-09-29T06:53:00.000Z","ordinal":0,"type":"session_meta","payload":{"id":"` + childID + `","timestamp":"2026-09-29T06:53:00.000Z","cwd":"/src/codex","originator":"codex-tui","source":{"sub_agent":"thread_spawn"},"thread_source":"user"}}
{"timestamp":"2026-09-29T06:53:01.000Z","ordinal":1,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Review the diff for bugs"}]}}
{"timestamp":"2026-09-29T06:53:05.000Z","ordinal":2,"type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"const r = await tools.exec_command({cmd:\"go vet ./...\",yield_time_ms:10000}); text(r.output);\n"}}
{"timestamp":"2026-09-29T06:53:09.000Z","ordinal":3,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"No bugs."}]}}
`

const emptyRollout = `{"timestamp":"2026-09-29T07:00:00.000Z","ordinal":0,"type":"session_meta","payload":{"id":"` + emptyID + `","timestamp":"2026-09-29T07:00:00.000Z","cwd":"/src/codex","originator":"codex-tui","source":"cli"}}
`

func writeFixtures(t *testing.T) (*Adapter, map[string]string) {
	t.Helper()
	home := t.TempDir()
	day := filepath.Join(home, "sessions", "2026", "09", "29")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{
		parentID: filepath.Join(day, "rollout-2026-09-29T08-50-51-"+parentID+".jsonl"),
		childID:  filepath.Join(day, "rollout-2026-09-29T08-53-00-"+childID+".jsonl"),
		emptyID:  filepath.Join(day, "rollout-2026-09-29T09-00-00-"+emptyID+".jsonl"),
		dbOnlyID: filepath.Join(day, "rollout-2026-09-29T09-30-00-"+dbOnlyID+".jsonl"),
	}
	for id, content := range map[string]string{parentID: parentRollout, childID: childRollout, emptyID: emptyRollout} {
		if err := os.WriteFile(paths[id], []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := filepath.Join(home, "state_5.sqlite")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE threads (
		id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
		source TEXT NOT NULL, cwd TEXT NOT NULL, title TEXT NOT NULL, first_user_message TEXT NOT NULL DEFAULT '',
		name TEXT, thread_source TEXT, agent_role TEXT, created_at_ms INTEGER, updated_at_ms INTEGER);
		CREATE TABLE thread_spawn_edges (parent_thread_id TEXT NOT NULL, child_thread_id TEXT NOT NULL PRIMARY KEY, status TEXT NOT NULL);
		INSERT INTO threads VALUES ('` + parentID + `', '` + paths[parentID] + `', 1790664651, 1790664750, 'cli', '/src/codex',
			'implement the feature', 'implement the feature', 'Implement the feature', 'user', NULL, 1790664651737, 1790664750714);
		INSERT INTO threads VALUES ('` + childID + `', '` + paths[childID] + `', 1790664780, 1790664789, 'subAgent', '/src/codex',
			'Review the diff for bugs', 'Review the diff for bugs', NULL, 'user', 'worker', NULL, NULL);
		INSERT INTO threads VALUES ('` + dbOnlyID + `', '` + paths[dbOnlyID] + `', 1790667000, 1790667100, 'exec', '/src/other',
			'run the tests', 'run the tests', NULL, 'user', NULL, NULL, NULL);
		INSERT INTO thread_spawn_edges VALUES ('` + parentID + `', '` + childID + `', 'completed');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return &Adapter{HomeDir: home}, paths
}

func TestImport(t *testing.T) {
	a, paths := writeFixtures(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var commands []agent.ShellCommand
	n, err := a.Import(context.Background(), st, func(c agent.ShellCommand) { commands = append(commands, c) })
	if err != nil || n != 2 {
		t.Fatalf("Import = %d, %v; want 2 sessions (the empty rollout skipped)", n, err)
	}

	parent, ok, _ := st.Get("codex:" + parentID)
	if !ok {
		t.Fatal("parent session not imported")
	}
	if parent.Title != "Implement the feature" || parent.LastPrompt != "implement the feature" || parent.Cwd != "/src/codex" ||
		parent.Kind != store.KindInteractive || parent.Status != store.StatusExited || parent.ParentID != "" ||
		parent.CreatedAt != 1790664651737 || parent.UpdatedAt != 1790664750714 ||
		parent.Transcript != paths[parentID] {
		t.Errorf("parent = %+v", parent)
	}
	child, _, _ := st.Get("codex:" + childID)
	if child.ParentID != "codex:"+parentID || child.Kind != store.KindInternal || child.LastPrompt != "Review the diff for bugs" {
		t.Errorf("child = %+v", child)
	}
	dbOnly, ok, _ := st.Get("codex:" + dbOnlyID)
	if !ok || dbOnly.LastPrompt != "run the tests" || dbOnly.Cwd != "/src/other" || dbOnly.Kind != store.KindHeadless ||
		dbOnly.CreatedAt != 1790667000000 || dbOnly.UpdatedAt != 1790667100000 || dbOnly.Transcript != paths[dbOnlyID] {
		t.Errorf("thread known only to the state database = %+v (found %v)", dbOnly, ok)
	}
	if _, ok, _ := st.Get("codex:" + emptyID); ok {
		t.Error("a rollout with no message was listed")
	}

	wantCommands := []agent.ShellCommand{
		{SessionID: "codex:" + parentID, At: timestamp("2026-09-29T06:52:27.600Z"), Cwd: "/src/codex", Command: "claude -p review"},
		{SessionID: "codex:" + parentID, At: timestamp("2026-09-29T06:52:28.909Z"), Cwd: "/src/codex/backend", Command: "opencode run --title impl-be 'go'"},
	}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Errorf("commands = %+v\nwant %+v", commands, wantCommands)
	}

	if n, err := a.Import(context.Background(), st, func(agent.ShellCommand) {}); err != nil || n != 0 {
		t.Errorf("unchanged import = %d, %v; want no sessions", n, err)
	}
}

func TestTailAndResume(t *testing.T) {
	a, paths := writeFixtures(t)
	parent := store.Session{NativeID: parentID, Transcript: paths[parentID]}
	lines, err := a.Tail(parent, 10)
	want := []agent.Line{
		{Role: "user", Text: "implement the feature"},
		{Role: "tool", Text: "shell: claude -p review"},
		{Role: "assistant", Text: "Done."},
	}
	if err != nil || !reflect.DeepEqual(lines, want) {
		t.Errorf("Tail = %+v, %v\nwant %+v", lines, err, want)
	}
	if lines, err := a.Tail(parent, 1); err != nil || len(lines) != 1 || lines[0].Role != "assistant" {
		t.Errorf("Tail(1) = %+v, %v", lines, err)
	}
	child := store.Session{NativeID: childID}
	lines, err = a.Tail(child, 10)
	if err != nil || len(lines) != 3 || lines[1] != (agent.Line{Role: "tool", Text: "exec: go vet ./..."}) {
		t.Errorf("Tail without a recorded transcript = %+v, %v", lines, err)
	}
	if err := a.CheckResume(parent); err != nil {
		t.Errorf("saved session can't resume: %v", err)
	}
	if err := a.CheckResume(child); err != nil {
		t.Errorf("session found by ID can't resume: %v", err)
	}
	if err := a.CheckResume(store.Session{NativeID: "missing"}); err == nil || !strings.Contains(err.Error(), "removed") {
		t.Errorf("missing session file was allowed to resume: %v", err)
	}
}

func TestOldRolloutFormat(t *testing.T) {
	home := t.TempDir()
	day := filepath.Join(home, "sessions", "2025", "06", "01")
	os.MkdirAll(day, 0o755)
	id := "0f0e0d0c-0b0a-4908-8706-050403020100"
	path := filepath.Join(day, "rollout-2025-06-01T10-00-00-"+id+".jsonl")
	old := `{"id":"` + id + `","timestamp":"2025-06-01T10:00:00.000Z","instructions":"be brief","cwd":"/src/old","originator":"codex_exec"}
{"type":"message","role":"user","content":[{"type":"input_text","text":"list the files"}]}
{"type":"local_shell_call","action":{"type":"exec","command":["ls","-la"],"working_directory":"/src/old"}}
{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Here they are."}]}
`
	os.WriteFile(path, []byte(old), 0o600)
	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{HomeDir: home}
	var commands []agent.ShellCommand
	if n, err := a.Import(context.Background(), st, func(c agent.ShellCommand) { commands = append(commands, c) }); err != nil || n != 1 {
		t.Fatalf("Import = %d, %v", n, err)
	}
	s, _, _ := st.Get("codex:" + id)
	if s.Cwd != "/src/old" || s.Kind != store.KindHeadless || s.LastPrompt != "list the files" || s.CreatedAt != timestamp("2025-06-01T10:00:00.000Z") {
		t.Errorf("session = %+v", s)
	}
	if len(commands) != 1 || commands[0].Command != "ls -la" || commands[0].Cwd != "/src/old" {
		t.Errorf("commands = %+v", commands)
	}
	lines, err := a.Tail(s, 5)
	if err != nil || len(lines) != 3 || lines[1].Text != "shell: ls -la" {
		t.Errorf("Tail = %+v, %v", lines, err)
	}
}

func TestCommandString(t *testing.T) {
	for _, tt := range []struct{ json, want string }{
		{`["bash","-lc","claude -p 'x y'"]`, "claude -p 'x y'"},
		{`["/bin/zsh","-c","echo hi"]`, "echo hi"},
		{`["zsh","-l","-c","echo hi"]`, "echo hi"},
		{`["ls","-la","my dir"]`, "ls -la 'my dir'"},
		{`"opencode run x"`, "opencode run x"},
	} {
		if got := commandString(gjson.Parse(tt.json)); got != tt.want {
			t.Errorf("commandString(%s) = %q, want %q", tt.json, got, tt.want)
		}
	}
	if got := codeModeCommands(`await tools.exec_command({cmd:"go test ./...", yield_time_ms: 1}); tools.exec_command({cmd:'ls'})`); !reflect.DeepEqual(got, []string{"go test ./...", "ls"}) {
		t.Errorf("codeModeCommands = %q", got)
	}
	if got := fileURLPath("file:///src/my%20dir"); got != "/src/my dir" {
		t.Errorf("fileURLPath = %q", got)
	}
}
