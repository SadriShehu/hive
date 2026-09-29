package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/proc"
)

func parse(t *testing.T, payload string) []agent.Event {
	t.Helper()
	evs, err := New().ParseHook([]byte(payload), nil, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestSpec(t *testing.T) {
	spec := New().Spec()
	if spec.Name != "codex" || spec.ParentEnv != "CODEX_THREAD_ID" || len(spec.TitleFlags) != 0 ||
		strings.Join(spec.SessionFlags, ",") != "resume" {
		t.Fatalf("Spec = %+v", spec)
	}
	if got := agent.Expand(spec.New, map[string]string{"prompt": "hello"}); strings.Join(got, " ") != "codex hello" {
		t.Errorf("new command = %q", got)
	}
	if got := agent.Expand(spec.New, nil); strings.Join(got, " ") != "codex" {
		t.Errorf("new command without a prompt = %q", got)
	}
	if got := agent.Expand(spec.Resume, map[string]string{"id": "old-id", "prompt": "go on"}); strings.Join(got, " ") != "codex resume old-id go on" {
		t.Errorf("resume command = %q", got)
	}
	if !spec.IsHeadless([]string{"codex", "exec", "hi"}) || !spec.IsHeadless([]string{"codex", "review"}) ||
		spec.IsHeadless([]string{"codex", "resume", "abc"}) || spec.IsHeadless([]string{"codex", "exec the plan"}) {
		t.Fatal("headless markers don't match Codex")
	}
	daemon := proc.Proc{Args: strings.Fields("/Users/me/.codex/packages/app-server-daemon/bin/codex app-server --listen unix:// --managed-daemon")}
	tui := proc.Proc{Args: []string{"/opt/homebrew/bin/codex"}}
	resumed := proc.Proc{Args: strings.Fields("codex resume abc")}
	if spec.Matches(daemon) || !spec.Matches(tui) || !spec.Matches(resumed) {
		t.Fatal("the app-server daemon must not count as a session; the TUI must")
	}
}

func TestParseHook(t *testing.T) {
	tests := []struct {
		payload string
		want    agent.EventType
	}{
		{`{"session_id":"s","hook_event_name":"SessionStart","source":"startup","cwd":"/src","model":"gpt-6"}`, agent.Start},
		{`{"session_id":"s","hook_event_name":"UserPromptSubmit","prompt":"hi"}`, agent.Prompt},
		{`{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"shell","tool_input":{}}`, agent.Busy},
		{`{"session_id":"s","hook_event_name":"PostToolUse","tool_response":{"big":"payload"}}`, agent.Busy},
		{`{"session_id":"s","hook_event_name":"PermissionRequest","tool_name":"shell"}`, agent.Attention},
		{`{"session_id":"s","hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"done"}`, agent.Idle},
		{`{"session_id":"s","hook_event_name":"Interrupt"}`, agent.Idle},
		{`{"session_id":"s","hook_event_name":"PreCompact"}`, agent.Busy},
		{`{"session_id":"s","hook_event_name":"SessionEnd","reason":"exit"}`, agent.End},
		{`{"session_id":"s","hook_event_name":"SomethingNew"}`, agent.Update},
	}
	for _, tt := range tests {
		evs := parse(t, tt.payload)
		if len(evs) != 1 || evs[0].Type != tt.want || evs[0].Tool != "codex" || evs[0].SessionID != "s" {
			t.Errorf("%s → %+v, want one %s event", tt.payload, evs, tt.want)
		}
	}
	start := parse(t, `{"session_id":"s","hook_event_name":"SessionStart","cwd":"/src","transcript_path":"/r.jsonl"}`)
	if start[0].Cwd != "/src" || start[0].Transcript != "/r.jsonl" {
		t.Errorf("SessionStart = %+v", start[0])
	}
	if evs := parse(t, `{"session_id":"s","hook_event_name":"PostToolUse","cwd":"/src/sub","transcript_path":null}`); evs[0].Cwd != "" || evs[0].Transcript != "" {
		t.Errorf("PostToolUse = %+v: only SessionStart knows the project folder", evs[0])
	}
	if prompt := parse(t, `{"session_id":"s","hook_event_name":"UserPromptSubmit","prompt":"fix the tests"}`); prompt[0].Prompt != "fix the tests" {
		t.Errorf("prompt = %q", prompt[0].Prompt)
	}
	generic := parse(t, `{"event":"busy","session_id":"s4","at":7}`)
	if len(generic) != 1 || generic[0].Type != agent.Busy || generic[0].At != 7 {
		t.Errorf("generic event = %+v", generic)
	}
	if _, err := New().ParseHook([]byte(`{"hook_event_name":"Stop"}`), nil, os.Getenv); err == nil {
		t.Error("payload without session_id accepted")
	}
}

func TestSubagentsAreInternalChildren(t *testing.T) {
	start := parse(t, `{"session_id":"sess","hook_event_name":"SubagentStart","agent_id":"child-thread","agent_type":"worker","cwd":"/src","agent_transcript_path":"/child.jsonl"}`)
	if len(start) != 2 || start[0].Type != agent.Start || start[1].Type != agent.Busy {
		t.Fatalf("SubagentStart → %+v, want start then busy", start)
	}
	ev := start[0]
	if ev.SessionID != "child-thread" || ev.ParentID != "codex:sess" || !ev.Internal || ev.Title != "worker" ||
		ev.Cwd != "/src" || ev.Transcript != "/child.jsonl" {
		t.Fatalf("SubagentStart event = %+v", ev)
	}
	stop := parse(t, `{"session_id":"sess","hook_event_name":"SubagentStop","agent_id":"child-thread"}`)
	if len(stop) != 1 || stop[0].Type != agent.End || stop[0].SessionID != "child-thread" || !stop[0].Internal {
		t.Fatalf("SubagentStop → %+v", stop)
	}
	if evs := parse(t, `{"session_id":"sess","hook_event_name":"SubagentStart"}`); len(evs) != 0 {
		t.Errorf("subagent without an ID reported: %+v", evs)
	}
}

func TestInstallRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	original := `{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "echo custom"}]}
    ]
  },
  "unrelated": {"keep": true}
}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{HooksPath: path}
	if a.Installed() {
		t.Fatal("installed before install")
	}
	msg, err := a.Install("/opt/hive")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "trust") {
		t.Errorf("install message doesn't tell the user to trust the hooks: %q", msg)
	}
	if !a.Installed() {
		t.Fatal("not installed")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Unrelated map[string]bool `json:"unrelated"`
		Hooks     map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Unrelated["keep"] {
		t.Fatalf("unrelated config changed: %s", data)
	}
	for _, event := range hookEvents {
		found := false
		for _, entry := range got.Hooks[event] {
			for _, hook := range entry.Hooks {
				found = found || hook.Command == "/opt/hive hook codex" && hook.Type == "command" && hook.Timeout == 10
			}
		}
		if !found {
			t.Errorf("%s hook missing: %s", event, data)
		}
	}
	if got.Hooks["SessionStart"][0].Hooks[0].Command != "echo custom" {
		t.Fatal("existing SessionStart hook was changed")
	}
	backup, err := os.ReadFile(path + ".bak-hive")
	if err != nil || string(backup) != original {
		t.Fatalf("backup = %q, %v", backup, err)
	}
	if msg, err := a.Install("/opt/hive"); err != nil || !strings.Contains(msg, "already installed") {
		t.Fatalf("repeat install = %q, %v", msg, err)
	}
	if msg, err := a.Install("/usr/local/bin/hive"); err != nil || !strings.Contains(msg, "updated 9") {
		t.Fatalf("install from a moved binary = %q, %v", msg, err)
	}
	if _, err := a.Uninstall(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !sameJSON(t, after, []byte(original)) || a.Installed() {
		t.Fatalf("uninstall changed user config: %s", after)
	}
}

func TestInstallCreatesAndRemovesHookFile(t *testing.T) {
	a := &Adapter{HooksPath: filepath.Join(t.TempDir(), "hooks.json")}
	if _, err := a.Install("/bin/hive"); err != nil {
		t.Fatal(err)
	}
	if !a.Installed() {
		t.Fatal("not installed")
	}
	if msg, err := a.Uninstall(); err != nil || !strings.Contains(msg, "removed 9 hooks and") {
		t.Fatalf("uninstall = %q, %v", msg, err)
	}
	if _, err := os.Stat(a.hooksPath()); !os.IsNotExist(err) {
		t.Fatalf("hook file remains after uninstall: %v", err)
	}
	if msg, err := a.Uninstall(); err != nil || !strings.Contains(msg, "not installed") {
		t.Fatalf("repeat uninstall = %q, %v", msg, err)
	}
}

func TestInstallRefusesBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	os.WriteFile(path, []byte(`{"hooks": [`), 0o600)
	if _, err := (&Adapter{HooksPath: path}).Install("/bin/hive"); err == nil {
		t.Fatal("invalid JSON was overwritten")
	}
}

func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(x, y)
}
