package codex

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

var usageRolloutLines = []string{
	`{"timestamp":"2026-09-29T06:52:24.919Z","type":"session_meta","payload":{"id":"` + parentID + `","cwd":"/src/codex","originator":"codex-tui","source":"cli"}}`,
	`{"timestamp":"2026-09-29T06:52:24.939Z","type":"turn_context","payload":{"turn_id":"t1","cwd":"/src/codex","model":"gpt-6-luna","effort":"xhigh"}}`,
	`{"timestamp":"2026-09-29T06:52:25.000Z","type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{}"}}`,
	`{"timestamp":"2026-09-29T06:52:26.000Z","type":"token_usage_record","payload":{"response_id":"resp_1","usage":{"input_tokens":1000,"cached_input_tokens":600,"cache_write_input_tokens":50,"output_tokens":200,"reasoning_output_tokens":80,"total_tokens":1200}}}`,
	`{"timestamp":"2026-09-29T06:52:26.100Z","type":"token_usage_record","payload":{"response_id":"resp_1","usage":{"input_tokens":1000,"cached_input_tokens":600,"cache_write_input_tokens":50,"output_tokens":200,"reasoning_output_tokens":80,"total_tokens":1200}}}`,
	`{"timestamp":"2026-09-29T06:52:27.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":200,"reasoning_output_tokens":80,"total_tokens":1200},"last_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":200,"total_tokens":1200},"model_context_window":258400}}}`,
	`{"timestamp":"2026-09-29T06:52:28.000Z","type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"x"}}`,
	`{"timestamp":"2026-09-29T06:52:28.500Z","type":"response_item","payload":{"type":"local_shell_call","action":{"command":["ls"]}}}`,
	`{"timestamp":"2026-09-29T06:52:29.000Z","type":"token_usage_record","payload":{"response_id":"resp_2","usage":{"input_tokens":3000,"cached_input_tokens":2500,"cache_write_input_tokens":0,"output_tokens":100,"reasoning_output_tokens":0,"total_tokens":3100}}}`,
	`{"timestamp":"2026-09-29T06:52:30.000Z","type":"compacted","payload":{}}`,
}

func writeRollout(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout-2026-09-29T08-50-51-"+parentID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadUsage(t *testing.T) {
	path := writeRollout(t, usageRolloutLines)
	s := store.Session{ID: "codex:" + parentID, Tool: "codex", NativeID: parentID, Transcript: path}
	a := &Adapter{}
	u, cursor, err := a.ReadUsage(context.Background(), s, store.Usage{}, "")
	if err != nil {
		t.Fatal(err)
	}
	tokens := store.Tokens{Input: 900, CacheRead: 3100, CacheWrite: 50, Output: 300, Reasoning: 80}
	want := store.Usage{
		ID: "codex:" + parentID, Model: "gpt-6-luna", Effort: "xhigh",
		Models:        map[string]store.Tokens{"gpt-6-luna": tokens},
		Tokens:        tokens,
		ContextTokens: 3000, ContextWindow: 258_400,
		Tools:    map[string]int{"shell": 2, "exec": 1},
		Requests: 2, Compactions: 1,
	}
	if !reflect.DeepEqual(u, want) {
		t.Errorf("usage =\n%+v\nwant\n%+v", u, want)
	}
	again, cursor2, err := a.ReadUsage(context.Background(), s, u, cursor)
	if err != nil || cursor2 != cursor || !reflect.DeepEqual(again, u) {
		t.Errorf("an unchanged file was re-read: %v", err)
	}
}

func TestReadUsageIncrementalMatchesOnePass(t *testing.T) {
	path := writeRollout(t, usageRolloutLines[:5])
	s := store.Session{ID: "codex:" + parentID, Tool: "codex", NativeID: parentID, Transcript: path}
	a := &Adapter{}
	first, cursor, err := a.ReadUsage(context.Background(), s, store.Usage{}, "")
	if err != nil || first.Requests != 1 {
		t.Fatalf("first = %+v, %v", first, err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(usageRolloutLines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, _, err := a.ReadUsage(context.Background(), s, first, cursor)
	if err != nil {
		t.Fatal(err)
	}
	onePass, _, _ := a.ReadUsage(context.Background(), s, store.Usage{}, "")
	if !reflect.DeepEqual(second, onePass) {
		t.Errorf("incremental =\n%+v\none pass\n%+v", second, onePass)
	}
}

func TestOldRolloutsCountTokenCountDeltas(t *testing.T) {
	lines := []string{
		usageRolloutLines[1],
		`{"timestamp":"2026-09-29T06:52:27.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":200,"total_tokens":1200},"last_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":200,"total_tokens":1200},"model_context_window":258400}}}`,
		`{"timestamp":"2026-09-29T06:52:28.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":2000,"cached_input_tokens":1500,"output_tokens":300,"total_tokens":2300},"last_token_usage":{"input_tokens":1000,"cached_input_tokens":900,"output_tokens":100,"total_tokens":1100},"model_context_window":258400}}}`,
		`{"timestamp":"2026-09-29T06:52:29.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":500,"cached_input_tokens":100,"output_tokens":50,"total_tokens":550},"last_token_usage":{"input_tokens":500,"cached_input_tokens":100,"output_tokens":50,"total_tokens":550},"model_context_window":258400}}}`,
	}
	path := writeRollout(t, lines)
	s := store.Session{ID: "codex:" + parentID, NativeID: parentID, Transcript: path}
	u, _, err := (&Adapter{}).ReadUsage(context.Background(), s, store.Usage{}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := store.Tokens{Input: 900, CacheRead: 1600, Output: 350}
	if u.Tokens != want || u.Requests != 3 || u.ContextTokens != 500 {
		t.Errorf("usage = %+v, want tokens %+v", u, want)
	}
}

func TestReadUsageWithoutRollout(t *testing.T) {
	a := &Adapter{SessionsDir: t.TempDir()}
	if _, _, err := a.ReadUsage(context.Background(), store.Session{NativeID: emptyID}, store.Usage{}, ""); err != agent.ErrNoUsage {
		t.Errorf("missing rollout: %v", err)
	}
}
