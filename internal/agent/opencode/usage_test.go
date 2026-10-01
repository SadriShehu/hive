package opencode

import (
	"context"
	"reflect"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

func TestReadUsage(t *testing.T) {
	db, path := fakeDB(t)
	mustExec(t, db, `CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
		time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL)`)
	mustExec(t, db, `INSERT INTO session VALUES ('ses_top', NULL, '/src/app', 'work', 1000, 5000)`)
	mustExec(t, db, `INSERT INTO message VALUES
		('m1', 'ses_top', 2000, 2000, '{"role":"user","model":{"providerID":"deepseek","modelID":"deepseek-v4-pro"},"summary":true}'),
		('m2', 'ses_top', 2100, 2100, '{"role":"assistant","providerID":"deepseek","modelID":"deepseek-v4-pro","variant":"max","cost":0.002,"tokens":{"total":1300,"input":1000,"output":200,"reasoning":100,"cache":{"read":0,"write":50}}}'),
		('m3', 'ses_top', 2200, 2200, '{"role":"assistant","providerID":"deepseek","modelID":"deepseek-v4-pro","variant":"max","cost":0.003,"tokens":{"total":2900,"input":1500,"output":300,"reasoning":100,"cache":{"read":1000,"write":20}}}')`)
	mustExec(t, db, `INSERT INTO part VALUES
		('p1', 'm2', 'ses_top', 2500, 2500, '{"type":"tool","tool":"read","state":{"input":{"filePath":"x"}}}'),
		('p2', 'm2', 'ses_top', 2600, 2600, '{"type":"tool","tool":"read","state":{"input":{"filePath":"y"}}}'),
		('p3', 'm3', 'ses_top', 2700, 2700, '{"type":"tool","tool":"bash","state":{"input":{"command":"go test ./..."}}}'),
		('p4', 'm3', 'ses_top', 2800, 2800, '{"type":"tool","tool":"skill","state":{"input":{"name":"review"}}}'),
		('p5', 'm3', 'ses_top', 2900, 2900, '{"type":"text","text":"done"}')`)

	a := &Adapter{DBPath: path}
	s := store.Session{ID: "opencode:ses_top", Tool: "opencode", NativeID: "ses_top"}
	u, cursor, err := a.ReadUsage(context.Background(), s, store.Usage{}, "")
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
		Tools:         map[string]int{"read": 2, "bash": 1, "skill": 1},
		Skills:        map[string]int{"review": 1},
		Requests:      2, Compactions: 1,
	}
	if !reflect.DeepEqual(u, want) {
		t.Errorf("usage =\n%+v\nwant\n%+v", u, want)
	}
	if cursor != "5000" {
		t.Errorf("cursor = %q", cursor)
	}
	again, cursor2, err := a.ReadUsage(context.Background(), s, u, cursor)
	if err != nil || cursor2 != cursor || !reflect.DeepEqual(again, u) {
		t.Errorf("an unchanged session was re-read: %v", err)
	}
	if _, _, err := a.ReadUsage(context.Background(), store.Session{NativeID: "ses_gone"}, store.Usage{}, ""); err != agent.ErrNoUsage {
		t.Errorf("unknown session: %v", err)
	}
}

func TestReadUsageWithoutDatabase(t *testing.T) {
	a := &Adapter{DBPath: t.TempDir() + "/missing.db"}
	if _, _, err := a.ReadUsage(context.Background(), store.Session{NativeID: "x"}, store.Usage{}, ""); err != agent.ErrNoUsage {
		t.Errorf("missing db: %v", err)
	}
}
