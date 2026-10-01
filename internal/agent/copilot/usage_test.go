package copilot

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

var usageEventLines = []string{
	`{"id":"e1","type":"session.start","timestamp":"2026-09-28T08:00:00.000Z","data":{"sessionId":"s","selectedModel":"model-a","context":{"cwd":"/src"}}}`,
	`{"id":"e2","type":"assistant.message","timestamp":"2026-09-28T08:00:01.000Z","data":{"content":"hi","outputTokens":10}}`,
	`{"id":"e3","type":"tool.execution_start","timestamp":"2026-09-28T08:00:02.000Z","data":{"toolName":"bash","arguments":{"command":"ls"}}}`,
	`{"id":"e4","type":"tool.execution_start","timestamp":"2026-09-28T08:00:03.000Z","data":{"toolName":"bash","arguments":{"command":"pwd"}}}`,
	`{"id":"e5","type":"tool.execution_start","timestamp":"2026-09-28T08:00:04.000Z","data":{"toolName":"view","arguments":{"path":"x"}}}`,
	`{"id":"e6","type":"skill.invoked","timestamp":"2026-09-28T08:00:05.000Z","data":{"name":"review"}}`,
	`{"id":"e7","type":"assistant.message","timestamp":"2026-09-28T08:00:06.000Z","data":{"content":"done","outputTokens":20}}`,
	`{"id":"e8","type":"session.compaction_complete","timestamp":"2026-09-28T08:00:07.000Z","data":{"preCompactionTokens":5000}}`,
	`{"id":"e9","type":"session.shutdown","timestamp":"2026-09-28T08:00:08.000Z","data":{"totalPremiumRequests":1,"totalApiDurationMs":500,"currentModel":"model-a","currentTokens":4200,"modelMetrics":{"model-a":{"requests":{"count":2,"cost":1},"usage":{"inputTokens":1000,"outputTokens":30,"cacheReadTokens":800,"cacheWriteTokens":100,"reasoningTokens":5}}},"codeChanges":{"linesAdded":7,"linesRemoved":2}}}`,
	`{"id":"e10","type":"session.resume","timestamp":"2026-09-28T09:00:00.000Z","data":{"context":{"cwd":"/src"}}}`,
	`{"id":"e11","type":"session.model_change","timestamp":"2026-09-28T09:00:01.000Z","data":{"previousModel":"model-a","newModel":"model-b"}}`,
	`{"id":"e12","type":"assistant.message","timestamp":"2026-09-28T09:00:02.000Z","data":{"content":"again","outputTokens":5}}`,
}

func writeEvents(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadUsage(t *testing.T) {
	path := writeEvents(t, usageEventLines)
	s := store.Session{ID: "copilot:s", Tool: "copilot", NativeID: "s", Transcript: path}
	a := &Adapter{}
	u, cursor, err := a.ReadUsage(context.Background(), s, store.Usage{}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := store.Usage{
		ID: "copilot:s", Model: "model-b",
		Models: map[string]store.Tokens{
			"model-a": {Input: 100, Output: 30, CacheRead: 800, CacheWrite: 100, Reasoning: 5},
			"model-b": {Output: 5},
		},
		Tokens:        store.Tokens{Input: 100, Output: 35, CacheRead: 800, CacheWrite: 100, Reasoning: 5},
		ContextTokens: 4200,
		Tools:         map[string]int{"bash": 2, "view": 1},
		Skills:        map[string]int{"review": 1},
		Requests:      3, Compactions: 1,
		APIDurationMS: 500, LinesAdded: 7, LinesRemoved: 2,
		PremiumRequests: 1,
		Partial:         true,
	}
	if !reflect.DeepEqual(u, want) {
		t.Errorf("usage =\n%+v\nwant\n%+v", u, want)
	}

	closing := append([]string{}, usageEventLines...)
	closing = append(closing, `{"id":"e13","type":"session.shutdown","timestamp":"2026-09-28T09:00:03.000Z","data":{"totalPremiumRequests":0.5,"currentModel":"model-b","currentTokens":900,"modelMetrics":{"model-b":{"requests":{"count":1},"usage":{"inputTokens":300,"outputTokens":6,"cacheReadTokens":200,"cacheWriteTokens":0}}}}}`)
	if err := os.WriteFile(path, []byte(strings.Join(closing, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	u2, _, err := a.ReadUsage(context.Background(), s, u, cursor)
	if err != nil {
		t.Fatal(err)
	}
	if u2.Partial || u2.Requests != 3 || u2.PremiumRequests != 1.5 || u2.ContextTokens != 900 ||
		u2.Models["model-b"] != (store.Tokens{Input: 100, Output: 6, CacheRead: 200}) || u2.Tokens.Output != 36 {
		t.Errorf("after the second shutdown = %+v", u2)
	}
}

func TestReadUsageWithoutEvents(t *testing.T) {
	a := &Adapter{SessionStateDir: t.TempDir()}
	if _, _, err := a.ReadUsage(context.Background(), store.Session{NativeID: "gone"}, store.Usage{}, ""); err != agent.ErrNoUsage {
		t.Errorf("missing events: %v", err)
	}
}
