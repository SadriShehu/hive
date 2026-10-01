package claude

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

const fableUsage = `"usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":1000,"cache_read_input_tokens":5000,"cache_creation":{"ephemeral_5m_input_tokens":400,"ephemeral_1h_input_tokens":600},"output_tokens_details":{"thinking_tokens":20}}`

var usageTranscriptLines = []string{
	`{"type":"user","message":{"role":"user","content":"do it"},"timestamp":"2026-09-28T08:00:01.000Z","cwd":"/src/app","sessionId":"s1"}`,
	`{"type":"assistant","requestId":"req_1","effort":"xhigh","message":{"id":"msg_1","model":"claude-fable-5-1","content":[{"type":"thinking","thinking":"hm"}],` + fableUsage + `},"timestamp":"2026-09-28T08:00:05.000Z"}`,
	`{"type":"assistant","requestId":"req_1","message":{"id":"msg_1","model":"claude-fable-5-1","content":[{"type":"text","text":"On it."}],` + fableUsage + `}}`,
	`{"type":"assistant","requestId":"req_1","message":{"id":"msg_1","model":"claude-fable-5-1","content":[{"type":"tool_use","id":"t1","name":"Skill","input":{"skill":"seen:review"}}],` + fableUsage + `}}`,
	`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
	`{"type":"assistant","requestId":"req_2","message":{"id":"msg_2","model":"claude-fable-5-1","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"go test"}},{"type":"tool_use","id":"t3","name":"Agent","input":{"prompt":"x"}}],"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":6000}}}`,
	`{"type":"assistant","message":{"id":"msg_err","model":"<synthetic>","content":[{"type":"text","text":"error"}],"usage":{"input_tokens":0,"output_tokens":0}}}`,
	`{"type":"user","message":{"role":"user","content":"<command-message>review</command-message>\n<command-name>/review</command-name>"}}`,
	`{"type":"user","isCompactSummary":true,"message":{"role":"user","content":"summary"}}`,
	`{"type":"cost-state","sessionId":"s1","startTime":1790582400000,"totalCostUSD":1.5,"totalAPIDuration":4200,"totalLinesAdded":12,"totalLinesRemoved":3,"modelUsage":{"claude-fable-5-1":{"inputTokens":110,"outputTokens":55,"costUSD":1.5}}}`,
	`{"type":"assistant","requestId":"req_3","message":{"id":"msg_3","model":"claude-haiku-4-5-20251001","content":[{"type":"text","text":"bye"}],"usage":{"input_tokens":5,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":100}}}`,
}

func writeTranscript(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func usageAdapter(t *testing.T, settings string) *Adapter {
	t.Helper()
	a := &Adapter{SettingsPath: filepath.Join(t.TempDir(), "settings.json")}
	if settings != "" {
		if err := os.WriteFile(a.SettingsPath, []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func liveSession(path string) store.Session {
	return store.Session{ID: "claude:s1", Tool: "claude", NativeID: "s1", Transcript: path, PID: 42, Kind: store.KindInteractive}
}

func TestReadUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	writeTranscript(t, path, usageTranscriptLines)
	u, cursor, err := usageAdapter(t, "").ReadUsage(context.Background(), liveSession(path), store.Usage{}, "")
	if err != nil {
		t.Fatal(err)
	}
	fable := store.Tokens{Input: 110, Output: 55, CacheRead: 11000, CacheWrite: 1000, CacheWrite1h: 600, Reasoning: 20}
	haiku := store.Tokens{Input: 5, Output: 1, CacheRead: 100}
	want := store.Usage{
		ID: "claude:s1", Model: "claude-haiku-4-5-20251001",
		Models:  map[string]store.Tokens{"claude-fable-5-1": fable, "claude-haiku-4-5-20251001": haiku},
		Tokens:  fable.Add(haiku),
		CostUSD: 1.5, CostSource: store.CostTool,
		ContextTokens: 105, ContextWindow: 200_000,
		Tools:    map[string]int{"Skill": 1, "Bash": 1, "Agent": 1},
		Skills:   map[string]int{"seen:review": 1, "/review": 1},
		Requests: 3, Compactions: 1, Effort: "xhigh",
		APIDurationMS: 4200, LinesAdded: 12, LinesRemoved: 3,
		Partial: true,
	}
	if !reflect.DeepEqual(u, want) {
		t.Errorf("usage =\n%+v\nwant\n%+v", u, want)
	}
	if cursor == "" {
		t.Error("no cursor")
	}
	again, cursor2, err := usageAdapter(t, "").ReadUsage(context.Background(), liveSession(path), u, cursor)
	if err != nil || cursor2 != cursor || !reflect.DeepEqual(again, u) {
		t.Errorf("an unchanged file was re-read: %+v %q %v", again, cursor2, err)
	}
}

func TestReadUsageIncrementalMatchesOnePass(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	writeTranscript(t, path, usageTranscriptLines[:3])
	a := usageAdapter(t, "")
	first, cursor, err := a.ReadUsage(context.Background(), liveSession(path), store.Usage{}, "")
	if err != nil || first.Requests != 1 {
		t.Fatalf("first read = %+v, %v", first, err)
	}
	writeTranscript(t, path, usageTranscriptLines)
	second, _, err := a.ReadUsage(context.Background(), liveSession(path), first, cursor)
	if err != nil {
		t.Fatal(err)
	}
	onePass, _, _ := a.ReadUsage(context.Background(), liveSession(path), store.Usage{}, "")
	if !reflect.DeepEqual(second, onePass) {
		t.Errorf("incremental =\n%+v\none pass\n%+v", second, onePass)
	}
}

func TestReadUsageRestartsOnTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	writeTranscript(t, path, usageTranscriptLines)
	a := usageAdapter(t, "")
	full, cursor, _ := a.ReadUsage(context.Background(), liveSession(path), store.Usage{}, "")
	writeTranscript(t, path, usageTranscriptLines[:2])
	u, _, err := a.ReadUsage(context.Background(), liveSession(path), full, cursor)
	if err != nil || u.Requests != 1 || u.CostSource != "" {
		t.Errorf("after truncation = %+v, %v", u, err)
	}
}

func TestSubagentKeepsCostUnknownAndIsNeverPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-a9.jsonl")
	writeTranscript(t, path, usageTranscriptLines)
	s := liveSession(path)
	s.Kind, s.Status = store.KindInternal, store.StatusWorking
	u, _, err := usageAdapter(t, "").ReadUsage(context.Background(), s, store.Usage{}, "")
	if err != nil || u.CostSource != "" || u.CostUSD != 0 || u.Partial || u.Requests != 3 {
		t.Errorf("subagent usage = %+v, %v", u, err)
	}
}

func TestFinishedSessionIsNotPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	writeTranscript(t, path, usageTranscriptLines)
	s := liveSession(path)
	s.PID = 0
	u, _, _ := usageAdapter(t, "").ReadUsage(context.Background(), s, store.Usage{}, "")
	if u.Partial {
		t.Error("a finished session is partial")
	}
}

func TestContextWindowHeuristics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	writeTranscript(t, path, usageTranscriptLines)
	if u, _, _ := usageAdapter(t, `{"model":"fable[1m]"}`).ReadUsage(context.Background(), liveSession(path), store.Usage{}, ""); u.ContextWindow != 1_000_000 {
		t.Errorf("settings [1m]: window = %d", u.ContextWindow)
	}
	big := append([]string{}, usageTranscriptLines...)
	big = append(big, `{"type":"assistant","requestId":"req_9","message":{"id":"msg_9","model":"claude-fable-5-1","content":[],"usage":{"input_tokens":1000,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":250000}}}`)
	writeTranscript(t, path, big)
	if u, _, _ := usageAdapter(t, "").ReadUsage(context.Background(), liveSession(path), store.Usage{}, ""); u.ContextWindow != 1_000_000 || u.ContextTokens != 251_000 {
		t.Errorf("observed context: %+v", u)
	}
	marked := append([]string{}, usageTranscriptLines...)
	marked = append(marked, `{"type":"cost-state","startTime":1,"totalCostUSD":0.1,"modelUsage":{"claude-opus-5-5[1m]":{"costUSD":0.1}}}`)
	writeTranscript(t, path, marked)
	if u, _, _ := usageAdapter(t, "").ReadUsage(context.Background(), liveSession(path), store.Usage{}, ""); u.ContextWindow != 1_000_000 || u.CostUSD != 1.6 {
		t.Errorf("[1m] in the cost record: %+v", u)
	}
}

func TestCostRecordOfTheSameRunReplacesItself(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	twice := append([]string{}, usageTranscriptLines...)
	twice = append(twice, `{"type":"cost-state","startTime":1790582400000,"totalCostUSD":2.5,"totalAPIDuration":5000,"totalLinesAdded":20,"totalLinesRemoved":3,"modelUsage":{}}`)
	writeTranscript(t, path, twice)
	u, _, _ := usageAdapter(t, "").ReadUsage(context.Background(), liveSession(path), store.Usage{}, "")
	if u.CostUSD != 2.5 || u.APIDurationMS != 5000 || u.LinesAdded != 20 || u.Partial {
		t.Errorf("usage = %+v", u)
	}
}

func TestReadUsageWithoutTranscript(t *testing.T) {
	a := usageAdapter(t, "")
	if _, _, err := a.ReadUsage(context.Background(), store.Session{ID: "claude:x"}, store.Usage{}, ""); err != agent.ErrNoUsage {
		t.Errorf("no transcript: %v", err)
	}
	s := liveSession(filepath.Join(t.TempDir(), "gone.jsonl"))
	if _, _, err := a.ReadUsage(context.Background(), s, store.Usage{}, ""); err != agent.ErrNoUsage {
		t.Errorf("missing transcript: %v", err)
	}
}
