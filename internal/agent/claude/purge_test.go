package claude

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sadrishehu/hive/internal/agent/agenttest"
	"github.com/sadrishehu/hive/internal/store"
)

func TestPurgeDeletesWhatClaudeKeepsForTheSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	log := agenttest.FakeTool(t, "claude", "")
	const id, other = "11111111-2222-4333-8444-555555555555", "99999999-2222-4333-8444-555555555555"
	mine := []string{
		"projects/-src-app/" + id + ".jsonl",
		"projects/-src-app/" + id + "/tool-results/x.txt",
		"projects/-src-app/" + id + "/subagents/agent-def.jsonl",
		"file-history/" + id + "/f@v1",
		"session-env/" + id + "/sessionstart-hook-0.sh",
		"tasks/" + id + "/1.json",
	}
	subagent := []string{
		"projects/-src-app/" + id + "/subagents/agent-abc.jsonl",
		"projects/-src-app/" + id + "/subagents/agent-abc.meta.json",
	}
	others := []string{
		"projects/-src-app/" + other + ".jsonl",
		"projects/-src-other/" + other + "/subagents/agent-abc.jsonl",
		"file-history/" + other + "/f@v1",
		"history.jsonl",
	}
	agenttest.Files(t, dir, slices.Concat(mine, subagent, others)...)
	job := func(short, session string) {
		agenttest.Files(t, dir, "jobs/"+short+"/state.json")
		os.WriteFile(filepath.Join(dir, "jobs", short, "state.json"), []byte(`{"sessionId":"`+session+`"}`), 0o644)
	}
	job("11111111", id)
	job("99999999", "99999999-0000-4000-8000-000000000000") // another session's short ID clashes

	a := New()
	ctx := context.Background()
	if err := a.Purge(ctx, store.Session{NativeID: id + "/abc", Kind: store.KindInternal}); err != nil {
		t.Fatal(err)
	}
	if left := agenttest.Exists(dir, subagent...); len(left) > 0 || len(agenttest.Exists(dir, mine...)) != len(mine) {
		t.Fatalf("purging a subagent: its files left %v; its parent's = %v", left, agenttest.Exists(dir, mine...))
	}
	if log() != "" {
		t.Fatalf("a subagent ran %q", log())
	}
	if err := a.Purge(ctx, store.Session{NativeID: id}); err != nil {
		t.Fatal(err)
	}
	if left := agenttest.Exists(dir, mine...); len(left) > 0 {
		t.Errorf("left %v", left)
	}
	if left := agenttest.Exists(dir, others...); len(left) != len(others) {
		t.Errorf("deleted other sessions' files: only %v left", left)
	}
	if log() != "rm 11111111" {
		t.Errorf("claude ran %q, want the background session removed", log())
	}
	if err := a.Purge(ctx, store.Session{NativeID: id}); err != nil {
		t.Errorf("purging it again: %v", err)
	}
	if err := a.Purge(ctx, store.Session{NativeID: other}); err != nil || log() != "rm 11111111\nrm 11111111" {
		t.Errorf("a job of another session was removed: err=%v log=%q", err, log())
	}
	for _, bad := range []string{"", "..", "../x", id + "/../x", "*"} {
		if err := a.Purge(ctx, store.Session{NativeID: bad}); err == nil {
			t.Errorf("purged %q", bad)
		}
	}
}

func TestPurgeStopsWhenClaudeRmRefuses(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	agenttest.FakeTool(t, "claude", `echo "worktree has unpushed commits; pass --discard-unpushed abc@w1"; exit 1`)
	const id = "11111111-2222-4333-8444-555555555555"
	agenttest.Files(t, dir, "projects/p/"+id+".jsonl", "jobs/11111111/state.json")
	os.WriteFile(filepath.Join(dir, "jobs/11111111/state.json"), []byte(`{"sessionId":"`+id+`"}`), 0o644)
	err := New().Purge(context.Background(), store.Session{NativeID: id})
	if err == nil || err.Error() != "claude rm 11111111: worktree has unpushed commits; pass --discard-unpushed abc@w1" {
		t.Fatalf("err = %v", err)
	}
	if len(agenttest.Exists(dir, "projects/p/"+id+".jsonl")) != 1 {
		t.Error("the transcript went although claude rm refused")
	}
}
