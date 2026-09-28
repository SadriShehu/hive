package copilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
)

func TestSpec(t *testing.T) {
	spec := New().Spec()
	if spec.Name != "copilot" || spec.ParentEnv != "" ||
		strings.Join(spec.Headless, ",") != "-p,--prompt" ||
		strings.Join(spec.TitleFlags, ",") != "-n,--name" ||
		strings.Join(spec.SessionFlags, ",") != "-r,--resume,--session-id" {
		t.Fatalf("Spec = %+v", spec)
	}
	if got := agent.Expand(spec.New, map[string]string{"session": "new-id", "prompt": "hello"}); strings.Join(got, " ") !=
		"copilot --session-id new-id --interactive hello" {
		t.Errorf("new command = %q", got)
	}
	if got := agent.Expand(spec.Resume, map[string]string{"id": "old-id"}); strings.Join(got, " ") !=
		"copilot --resume old-id" {
		t.Errorf("resume command = %q", got)
	}
	if !spec.IsHeadless([]string{"copilot", "-p", "hi"}) || spec.IsHeadless([]string{"copilot", "--interactive"}) {
		t.Fatal("headless flags don't match Copilot CLI")
	}
}

func TestParseHook(t *testing.T) {
	a := &Adapter{SessionStateDir: "/sessions"}
	tests := []struct {
		payload string
		want    agent.EventType
		prompt  string
	}{
		{`{"sessionId":"s1","timestamp":1700000000123,"cwd":"/src","source":"startup","initialPrompt":"start here"}`, agent.Start, "start here"},
		{`{"sessionId":"s1","timestamp":1700000000124,"cwd":"/src","prompt":"fix the tests"}`, agent.Prompt, "fix the tests"},
		{`{"sessionId":"s1","timestamp":1700000000125,"cwd":"/src","toolName":"bash","toolArgs":{"command":"go test ./..."}}`, agent.Busy, ""},
		{`{"sessionId":"s1","timestamp":1700000000126,"cwd":"/src","toolName":"bash","toolResult":{"resultType":"success"}}`, agent.Busy, ""},
		{`{"sessionId":"s1","timestamp":1700000000127,"cwd":"/src","error":{"message":"rate limited"}}`, agent.Update, ""},
		{`{"sessionId":"s1","timestamp":1700000000128,"cwd":"/src","stopReason":"end_turn"}`, agent.Idle, ""},
		{`{"sessionId":"s1","timestamp":1700000000129,"cwd":"/src","reason":"complete"}`, agent.End, ""},
	}
	events := []string{"sessionStart", "userPromptSubmitted", "preToolUse", "postToolUse", "errorOccurred", "agentStop", "sessionEnd"}
	for i, tt := range tests {
		evs, err := a.ParseHook([]byte(tt.payload), []string{events[i]}, os.Getenv)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 1 || evs[0].Type != tt.want || evs[0].SessionID != "s1" ||
			evs[0].Tool != name || evs[0].Prompt != tt.prompt || evs[0].Cwd != "/src" {
			t.Errorf("ParseHook(%s) = %+v, want %s", tt.payload, evs, tt.want)
		}
		if evs[0].At != 1700000000123+int64(i) {
			t.Errorf("timestamp = %d", evs[0].At)
		}
	}
	vsCode, err := a.ParseHook([]byte(`{"hook_event_name":"SessionStart","session_id":"s2","timestamp":"2026-09-28T12:00:00Z","cwd":"/work"}`), nil, os.Getenv)
	if err != nil || len(vsCode) != 1 || vsCode[0].Type != agent.Start || vsCode[0].At != 1790596800000 {
		t.Fatalf("VS Code payload = %+v, %v", vsCode, err)
	}
	child, err := a.ParseHook([]byte(`{"sessionId":"s3","parentId":"s2","event":"sessionStart"}`), nil, os.Getenv)
	if err != nil || child[0].ParentID != "copilot:s2" ||
		child[0].Transcript != filepath.Join("/sessions", "s3", "events.jsonl") {
		t.Fatalf("child payload = %+v, %v", child, err)
	}
	generic, err := a.ParseHook([]byte(`{"event":"busy","session_id":"s4","at":7,"prompt":"work"}`), nil, os.Getenv)
	if err != nil || len(generic) != 1 || generic[0].Type != agent.Busy || generic[0].At != 7 {
		t.Fatalf("generic event = %+v, %v", generic, err)
	}
	if _, err := a.ParseHook([]byte(`{"event":"sessionStart"}`), nil, os.Getenv); err == nil {
		t.Fatal("payload without a session ID accepted")
	}
}

func TestInstallRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks", "hive.json")
	original := `{
  "version": 1,
  "hooks": {
    "sessionStart": [
      {"type":"command","bash":"echo custom"}
    ]
  },
  "unrelated": {"keep": true}
}`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{HooksDir: filepath.Dir(path)}
	if a.Installed() {
		t.Fatal("installed before install")
	}
	if _, err := a.Install("/opt/hive"); err != nil {
		t.Fatal(err)
	}
	if !a.Installed() {
		t.Fatal("not installed")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Version   int             `json:"version"`
		Unrelated map[string]bool `json:"unrelated"`
		Hooks     map[string][]struct {
			Type string `json:"type"`
			Bash string `json:"bash"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || !got.Unrelated["keep"] {
		t.Fatalf("unrelated config changed: %s", data)
	}
	for _, event := range hookEvents {
		var found bool
		for _, hook := range got.Hooks[event] {
			found = found || hook.Bash == agent.ShellQuote("/opt/hive")+" hook copilot "+event
		}
		if !found {
			t.Errorf("%s hook missing: %s", event, data)
		}
	}
	if got.Hooks["sessionStart"][0].Bash != "echo custom" {
		t.Fatal("existing sessionStart hook was changed")
	}
	backup, err := os.ReadFile(path + ".bak-hive")
	if err != nil || string(backup) != original {
		t.Fatalf("backup = %q, %v", backup, err)
	}
	if msg, err := a.Install("/opt/hive"); err != nil || !strings.Contains(msg, "already installed") {
		t.Fatalf("repeat install = %q, %v", msg, err)
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
	a := &Adapter{HooksDir: t.TempDir()}
	if _, err := a.Install("/bin/hive"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.hooksPath()); !os.IsNotExist(err) {
		t.Fatalf("hook file remains after uninstall: %v", err)
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
