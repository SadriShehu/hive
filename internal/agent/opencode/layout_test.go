package opencode

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

func fakeOpenCode2DB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, path := fakeDB(t)
	mustExec(t, db, `CREATE TABLE session_v2 (id TEXT PRIMARY KEY, parent_id TEXT, fork_session_id TEXT,
			directory TEXT NOT NULL, title TEXT, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL);
		CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, type TEXT NOT NULL,
			seq INTEGER NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL)`)
	mustExec(t, db, `INSERT INTO session_v2 VALUES
		('ses_top', NULL, NULL, '/src/app', 'Greeting', 1000, 5000),
		('ses_sub', 'ses_top', NULL, '/src/app', 'explore the handlers', 2000, 3000),
		('ses_untitled', NULL, NULL, '/src/app', NULL, 2000, 3000)`)
	mustExec(t, db, `INSERT INTO session_message VALUES
		('m1', 'ses_top', 'user', 1, 2000, 2000, '{"text":"run the tests","files":[],"agents":[]}'),
		('m2', 'ses_top', 'assistant', 2, 2100, 2100, '{"model":{"id":"deepseek-v4-pro","providerID":"deepseek","variant":"max"},"content":[{"type":"reasoning","text":"think"},{"type":"tool","name":"shell","state":{"status":"completed","input":{"command":"go test ./..."}}},{"type":"tool","name":"read","state":{"input":{"path":"main.go"}}}],"cost":0.002,"tokens":{"input":1000,"output":200,"reasoning":100,"cache":{"read":0,"write":50}}}'),
		('m3', 'ses_top', 'compaction', 3, 2150, 2150, '{"status":"completed"}'),
		('m4', 'ses_top', 'assistant', 4, 2200, 2200, '{"model":{"id":"deepseek-v4-pro","providerID":"deepseek","variant":"max"},"content":[{"type":"tool","name":"skill","state":{"input":{"id":"review"}}},{"type":"tool","name":"bash","state":{"input":{"command":"claude -p hi","workdir":"/src/app/web"}}},{"type":"text","text":"done"}],"cost":0.003,"tokens":{"input":1500,"output":300,"reasoning":100,"cache":{"read":1000,"write":20}}}'),
		('m5', 'ses_top', 'idle', 5, 2300, 2300, '{"outcome":"succeeded"}')`)
	return db, path
}

func TestImportReadsOpenCode2Sessions(t *testing.T) {
	_, path := fakeOpenCode2DB(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var cmds []agent.ShellCommand
	n, err := (&Adapter{DBPath: path}).Import(context.Background(), st, func(c agent.ShellCommand) { cmds = append(cmds, c) })
	if err != nil || n != 3 {
		t.Fatalf("Import = %d, %v", n, err)
	}
	top, _, _ := st.Get("opencode:ses_top")
	if top.Title != "Greeting" || top.Cwd != "/src/app" || top.ParentID != "" {
		t.Errorf("top = %+v", top)
	}
	sub, _, _ := st.Get("opencode:ses_sub")
	if sub.ParentID != "opencode:ses_top" || sub.Kind != store.KindInternal {
		t.Errorf("sub = %+v", sub)
	}
	want := []agent.ShellCommand{
		{SessionID: "opencode:ses_top", At: 2100, Cwd: "/src/app", Command: "go test ./..."},
		{SessionID: "opencode:ses_top", At: 2200, Cwd: "/src/app/web", Command: "claude -p hi"},
	}
	if !reflect.DeepEqual(cmds, want) {
		t.Errorf("commands = %+v", cmds)
	}
}

func TestTailReadsOpenCode2Messages(t *testing.T) {
	_, path := fakeOpenCode2DB(t)
	lines, err := (&Adapter{DBPath: path}).Tail(store.Session{NativeID: "ses_top"}, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []agent.Line{
		{Role: "tool", Text: "read: main.go"},
		{Role: "tool", Text: "skill: review"},
		{Role: "tool", Text: "bash: claude -p hi"},
		{Role: "assistant", Text: "done"},
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %+v", lines)
	}
}

func TestReadUsageReadsOpenCode2Messages(t *testing.T) {
	_, path := fakeOpenCode2DB(t)
	s := store.Session{ID: "opencode:ses_top", Tool: "opencode", NativeID: "ses_top"}
	u, cursor, err := (&Adapter{DBPath: path}).ReadUsage(context.Background(), s, store.Usage{}, "")
	if err != nil {
		t.Fatal(err)
	}
	tokens := store.Tokens{Input: 2500, Output: 700, Reasoning: 200, CacheRead: 1000, CacheWrite: 70}
	want := store.Usage{
		ID: "opencode:ses_top", Model: "deepseek/deepseek-v4-pro", Effort: "max",
		Models:  map[string]store.Tokens{"deepseek/deepseek-v4-pro": tokens},
		Tokens:  tokens,
		CostUSD: 0.005, CostSource: store.CostTool,
		ContextTokens: 2520,
		Tools:         map[string]int{"shell": 1, "read": 1, "skill": 1, "bash": 1},
		Skills:        map[string]int{"review": 1},
		Requests:      2, Compactions: 1,
	}
	if !reflect.DeepEqual(u, want) || cursor != "5000" {
		t.Errorf("cursor = %q, usage =\n%+v\nwant\n%+v", cursor, u, want)
	}
}

func TestOpenCode2PluginKeepsItsEventLoopAlive(t *testing.T) {
	_, eventLoop, found := strings.Cut(pluginSourceV2, "for await")
	if !found {
		t.Fatal("the opencode 2 plugin has no event loop")
	}
	eventLoop, _, _ = strings.Cut(eventLoop, "})()")
	if strings.Contains(eventLoop, "return") {
		t.Error("a return inside the event loop stops the plugin after the first event")
	}
	if !strings.Contains(pluginSourceV2, `event.tool === "shell"`) {
		t.Error("the opencode 2 plugin does not attribute shells to the shell tool")
	}
}
