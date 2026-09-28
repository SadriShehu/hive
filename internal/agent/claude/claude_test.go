package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

func parse(t *testing.T, payload string, env map[string]string) []agent.Event {
	t.Helper()
	evs, err := New().ParseHook([]byte(payload), nil, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestParseHook(t *testing.T) {
	tests := []struct {
		payload string
		want    agent.EventType
	}{
		{`{"session_id":"s","hook_event_name":"SessionStart","source":"startup"}`, agent.Start},
		{`{"session_id":"s","hook_event_name":"UserPromptSubmit","prompt":"hi"}`, agent.Prompt},
		{`{"session_id":"s","hook_event_name":"PostToolUse","tool_response":{"big":"payload"}}`, agent.Busy},
		{`{"session_id":"s","hook_event_name":"Notification","notification_type":"permission_prompt"}`, agent.Attention},
		{`{"session_id":"s","hook_event_name":"Notification","notification_type":"idle_prompt"}`, agent.Idle},
		{`{"session_id":"s","hook_event_name":"Notification","message":"Claude needs your permission to use Bash"}`, agent.Attention},
		{`{"session_id":"s","hook_event_name":"Notification","notification_type":"auth_success"}`, agent.Update},
		{`{"session_id":"s","hook_event_name":"Stop"}`, agent.Idle},
		{`{"session_id":"s","hook_event_name":"SessionEnd","reason":"exit"}`, agent.End},
	}
	for _, tt := range tests {
		evs := parse(t, tt.payload, nil)
		if len(evs) != 1 || evs[0].Type != tt.want || evs[0].Tool != "claude" || evs[0].SessionID != "s" {
			t.Errorf("%s → %+v, want one %s event", tt.payload, evs, tt.want)
		}
	}

	start := parse(t, `{"session_id":"s","hook_event_name":"SessionStart"}`, map[string]string{"CLAUDE_ENV_FILE": "/tmp/env"})
	if start[0].EnvFile != "/tmp/env" {
		t.Errorf("SessionStart EnvFile = %q, want CLAUDE_ENV_FILE", start[0].EnvFile)
	}
	prompt := parse(t, `{"session_id":"s","hook_event_name":"UserPromptSubmit","prompt":"fix the tests"}`, nil)
	if prompt[0].Prompt != "fix the tests" {
		t.Errorf("prompt = %q", prompt[0].Prompt)
	}
	if evs := parse(t, `{"session_id":"s","hook_event_name":"PostToolUse","cwd":"/src/app/sub"}`, nil); evs[0].Cwd != "" {
		t.Errorf("PostToolUse cwd = %q: only SessionStart knows the project folder", evs[0].Cwd)
	}
	if evs := parse(t, `{"session_id":"s","hook_event_name":"SubagentStart","agent_id":"x","agent_type":""}`, nil); len(evs) != 0 {
		t.Errorf("typeless helper subagent reported: %+v", evs)
	}
	if _, err := New().ParseHook([]byte(`{"hook_event_name":"Stop"}`), nil, os.Getenv); err == nil {
		t.Error("payload without session_id accepted")
	}
}

func TestSubagentsAreInternalChildren(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "sess.jsonl")
	sub := filepath.Join(dir, "sess", "subagents")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "agent-a1.meta.json"), []byte(`{"agentType":"general-purpose","description":"Scaffold rules"}`), 0o644)

	start := parse(t, `{"session_id":"sess","hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"general-purpose","transcript_path":"`+transcript+`"}`, nil)
	if len(start) != 2 || start[0].Type != agent.Start || start[1].Type != agent.Busy {
		t.Fatalf("SubagentStart → %+v, want start then busy", start)
	}
	ev := start[0]
	if ev.SessionID != "sess/a1" || ev.ParentID != "claude:sess" || !ev.Internal || ev.Title != "Scaffold rules" ||
		ev.Transcript != filepath.Join(sub, "agent-a1.jsonl") {
		t.Fatalf("SubagentStart event = %+v", ev)
	}
	stop := parse(t, `{"session_id":"sess","hook_event_name":"SubagentStop","agent_id":"a1","agent_transcript_path":"/x/agent-a1.jsonl"}`, nil)
	if len(stop) != 1 || stop[0].Type != agent.End || stop[0].SessionID != "sess/a1" || stop[0].Transcript != "/x/agent-a1.jsonl" {
		t.Fatalf("SubagentStop → %+v", stop)
	}
}

// userSettings mirrors the shape of a real settings.json with existing hooks.
const userSettings = `{
  "permissions": {
    "allow": ["Bash(go test:*)"]
  },
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "/opt/homebrew/bin/sketchybar --set claude icon.color=0x59ffffff 2>/dev/null || true"
          }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "permission_prompt|agent_needs_input|elicitation_dialog",
        "hooks": [
          {
            "type": "command",
            "command": "/opt/homebrew/bin/sketchybar --set claude icon.color=0xffff9f0a 2>/dev/null || true"
          }
        ]
      }
    ]
  },
  "theme": "dark"
}
`

func TestInstallRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte(userSettings), 0o600)
	a := &Adapter{SettingsPath: path}

	if a.Installed() {
		t.Fatal("Installed before install")
	}
	msg, err := a.Install("/Users/me/go/bin/hive")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "added 9 hooks") {
		t.Errorf("install message = %q", msg)
	}
	if !a.Installed() {
		t.Fatal("not Installed after install")
	}
	data, _ := os.ReadFile(path)
	var got struct {
		Permissions map[string]any
		Theme       string
		Hooks       map[string][]struct {
			Matcher string
			Hooks   []struct{ Command string }
		}
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("settings.json is no longer valid JSON: %v\n%s", err, data)
	}
	if got.Theme != "dark" || got.Permissions == nil {
		t.Errorf("unrelated settings lost: %s", data)
	}
	for _, ev := range hookEvents {
		entries := got.Hooks[ev]
		last := entries[len(entries)-1]
		if last.Hooks[0].Command != "/Users/me/go/bin/hive hook claude" || last.Matcher != "" {
			t.Errorf("%s: last entry = %+v, want hive's hook", ev, last)
		}
	}
	if n := len(got.Hooks["Stop"]); n != 2 {
		t.Errorf("Stop has %d entries, want sketchybar + hive", n)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("permissions changed to %v", fi.Mode().Perm())
	}
	if backup, _ := os.ReadFile(path + ".bak-hive"); string(backup) != userSettings {
		t.Error("backup does not hold the original file")
	}

	// Installing again changes nothing; a moved binary updates in place.
	if msg, _ := a.Install("/Users/me/go/bin/hive"); !strings.Contains(msg, "already installed") {
		t.Errorf("second install = %q", msg)
	}
	if msg, _ := a.Install("/opt/hive dir/hive"); !strings.Contains(msg, "updated 9") {
		t.Errorf("install from a new path = %q", msg)
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), `"'/opt/hive dir/hive' hook claude"`) {
		t.Errorf("new path not quoted into the command:\n%s", data)
	}

	if _, err := a.Uninstall(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !sameJSON(t, after, []byte(userSettings)) {
		t.Errorf("uninstall did not restore the original settings:\n%s", after)
	}
	if msg, _ := a.Uninstall(); !strings.Contains(msg, "not installed") {
		t.Errorf("second uninstall = %q", msg)
	}
}

func TestInstallCreatesSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude", "settings.json")
	a := &Adapter{SettingsPath: path}
	if _, err := a.Install("/bin/hive"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Uninstall(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.TrimSpace(string(data)) != "{}" {
		t.Errorf("after uninstall = %s, want {}", data)
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

func TestCheckResumeNeedsATranscript(t *testing.T) {
	dir := t.TempDir()
	saved := filepath.Join(dir, "saved.jsonl")
	os.WriteFile(saved, []byte("{}\n"), 0o600)
	a := New()
	if err := a.CheckResume(store.Session{Transcript: saved}); err != nil {
		t.Errorf("saved session refused: %v", err)
	}
	if err := a.CheckResume(store.Session{Transcript: filepath.Join(dir, "never-saved.jsonl")}); err == nil {
		t.Error("a session with no transcript was offered for reopening")
	}
}
