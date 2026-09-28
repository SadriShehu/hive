package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

// A transcript as Claude Code writes it, trimmed to the fields hive reads.
const transcriptLines = `{"type":"mode","mode":"normal","sessionId":"s1"}
{"type":"user","isMeta":true,"message":{"role":"user","content":"<local-command-caveat>ignore</local-command-caveat>"},"timestamp":"2026-09-28T08:00:00.000Z","cwd":"/src/app","entrypoint":"cli","sessionId":"s1"}
{"type":"user","message":{"role":"user","content":"implement the admin plan"},"timestamp":"2026-09-28T08:00:01.000Z","cwd":"/src/app","entrypoint":"cli","sessionId":"s1"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Starting two workers."},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"cd backend && opencode run --title impl-be 'go'"}}]},"timestamp":"2026-09-28T08:00:05.000Z","cwd":"/src/app","sessionId":"s1"}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"done"}]},"timestamp":"2026-09-28T08:05:00.000Z","cwd":"/src/app","sessionId":"s1"}
{"type":"ai-title","aiTitle":"Admin phase 2","sessionId":"s1"}
{"type":"last-prompt","lastPrompt":"ship it","sessionId":"s1"}
`

func TestImport(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "-src-app")
	os.MkdirAll(filepath.Join(project, "s1", "subagents"), 0o755)
	os.WriteFile(filepath.Join(project, "s1.jsonl"), []byte(transcriptLines), 0o600)
	os.WriteFile(filepath.Join(project, "empty.jsonl"), []byte(`{"type":"mode","mode":"normal"}`+"\n"), 0o600)
	os.WriteFile(filepath.Join(project, "s1", "subagents", "agent-a9.jsonl"),
		[]byte(`{"type":"user","isSidechain":true,"message":{"role":"user","content":"Review the diff for bugs"},"timestamp":"2026-09-28T08:01:00.000Z","cwd":"/src/app"}`+"\n"), 0o600)

	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := &Adapter{ProjectsDir: dir}
	var cmds []agent.ShellCommand
	n, err := a.Import(context.Background(), st, func(c agent.ShellCommand) { cmds = append(cmds, c) })
	if err != nil || n != 2 {
		t.Fatalf("Import = %d, %v; want 2 sessions (the empty transcript skipped)", n, err)
	}

	s, ok, _ := st.Get("claude:s1")
	if !ok {
		t.Fatal("session not imported")
	}
	if s.Title != "Admin phase 2" || s.LastPrompt != "ship it" || s.Cwd != "/src/app" || s.Kind != store.KindInteractive ||
		s.Status != store.StatusExited || s.CreatedAt != 1790582400000 || s.UpdatedAt != 1790582700000 {
		t.Errorf("session = %+v", s)
	}
	sub, _, _ := st.Get("claude:s1/a9")
	if sub.ParentID != "claude:s1" || sub.Kind != store.KindInternal || sub.Title != "Review the diff for bugs" {
		t.Errorf("subagent = %+v", sub)
	}
	if len(cmds) != 1 || cmds[0].SessionID != "claude:s1" || cmds[0].Cwd != "/src/app" ||
		cmds[0].At != 1790582405000 || !strings.Contains(cmds[0].Command, "opencode run") {
		t.Errorf("commands = %+v", cmds)
	}

	// Nothing changed, nothing re-read.
	if n, _ := a.Import(context.Background(), st, func(agent.ShellCommand) {}); n != 0 {
		t.Errorf("second import read %d sessions, want 0", n)
	}
}

func TestUserText(t *testing.T) {
	for line, want := range map[string]string{
		`{"message":{"content":"fix the build"}}`:                         "fix the build",
		`{"message":{"content":[{"type":"text","text":"from the IDE"}]}}`: "from the IDE",
		`{"message":{"content":[{"type":"tool_result","content":"x"}]}}`:  "",
		`{"message":{"content":"<command-name>/clear</command-name>"}}`:   "",
		`{"isMeta":true,"message":{"content":"injected"}}`:                "",
		`{"message":{"content":"[Request interrupted by user]"}}`:         "",
	} {
		if got := userText([]byte(line)); got != want {
			t.Errorf("userText(%s) = %q, want %q", line, got, want)
		}
	}
}
