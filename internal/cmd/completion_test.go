package cmd

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sadrishehu/hive/internal/store"
)

// complete runs hive's hidden __complete command and returns the values,
// without the trailing directive line.
func complete(t *testing.T, args ...string) []string {
	t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[len(lines)-1], ":") {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

// completionFixture fills a temporary database with two sessions, a team with
// a lead and a backend, and two tasks.
func completionFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HIVE_HOME", dir)
	t.Setenv("HIVE_CONFIG", filepath.Join(dir, "config.toml"))
	st, err := store.Open(filepath.Join(dir, "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UnixMilli()
	for _, s := range []store.Session{
		{ID: "claude:live0001abcdef", Tool: "claude", NativeID: "live0001abcdef", Title: "Audit", Status: store.StatusWorking, PID: 42, UpdatedAt: now - 2*60*60*1000, CreatedAt: now},
		{ID: "claude:recent02xyz", Tool: "claude", NativeID: "recent02xyz", Title: "Port tests", Status: store.StatusExited, UpdatedAt: now - 60*60*1000, CreatedAt: now},
		{ID: "claude:old00003xyz", Tool: "claude", NativeID: "old00003xyz", Title: "Old", Status: store.StatusExited, UpdatedAt: now - 48*60*60*1000, CreatedAt: now},
		{ID: "opencode:seed0001abc", Tool: "opencode", NativeID: "seed0001abc", Title: "Seed", Status: store.StatusExited, UpdatedAt: now, CreatedAt: now},
	} {
		if err := st.Upsert(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertUsage(store.UsageRecord{Usage: store.Usage{ID: "opencode:seed0001abc", Model: "test-model"}}); err != nil {
		t.Fatal(err)
	}
	api := store.Team{ID: "t1", Name: "api", Goal: "Build the admin API for everyone", Cwd: "/work", Status: store.TeamActive, CreatedAt: now - 10, UpdatedAt: now}
	sunset := store.Team{ID: "t2", Name: "sunset", Goal: "Ship the old thing", Cwd: "/work", Status: store.TeamDone, CreatedAt: now, UpdatedAt: now}
	for _, tm := range []store.Team{api, sunset} {
		if err := st.CreateTeam(tm); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []store.Member{
		{TeamID: api.ID, Role: "lead", SessionID: "claude:live0001abcdef", Tool: "claude", JoinedAt: 1},
		{TeamID: api.ID, Role: "backend", SessionID: "claude:recent02xyz", Tool: "claude", JoinedAt: 2},
	} {
		if err := st.AddMember(m); err != nil {
			t.Fatal(err)
		}
	}
	for _, task := range []store.Task{
		{TeamID: api.ID, N: 4, Title: "handlers for /admin", Status: store.TaskClaimed, Owner: "lead", CreatedAt: now, UpdatedAt: now},
		{TeamID: api.ID, N: 9, Title: "old thing", Status: store.TaskDone, Owner: "lead", CreatedAt: now, UpdatedAt: now},
	} {
		if _, err := st.AddTask(task); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCompletionSaysNothingWithoutADatabase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HIVE_HOME", dir)
	t.Setenv("HIVE_CONFIG", filepath.Join(dir, "config.toml"))
	if got := complete(t, "__complete", "send", ""); len(got) != 0 {
		t.Errorf("send completions = %q, want none", got)
	}
}

func TestCompletionListsLiveSessionsFirstInShortForm(t *testing.T) {
	completionFixture(t)
	got := complete(t, "__complete", "send", "")
	live, recent := -1, -1
	for i, v := range got {
		switch {
		case strings.HasPrefix(v, "claude:live0001\t"):
			live = i
		case strings.HasPrefix(v, "claude:recent02\t"):
			recent = i
		}
		if strings.HasPrefix(v, "claude:old00003") {
			t.Errorf("a session older than a day was completed: %q", v)
		}
	}
	if live < 0 || recent < 0 {
		t.Fatalf("completions = %q, want the live and recent short IDs", got)
	}
	if live > recent {
		t.Errorf("live session at %d comes after recent at %d: %q", live, recent, got)
	}
	if !strings.Contains(got[live], "lead") || !strings.Contains(got[live], "#1 handlers for /admin") || !strings.Contains(got[live], "working") {
		t.Errorf("live session description = %q, want the role, held task and state", got[live])
	}
}

func TestCompletionListsTeamsWithTheGoal(t *testing.T) {
	completionFixture(t)
	got := complete(t, "__complete", "team", "board", "")
	if len(got) < 2 {
		t.Fatalf("team completions = %q, want two teams", got)
	}
	if !strings.HasPrefix(got[0], "api\t") || !strings.Contains(got[0], "Build the admin API") {
		t.Errorf("first team = %q, want api with its goal", got[0])
	}
	if !strings.HasPrefix(got[1], "sunset\t") {
		t.Errorf("done team = %q, want sunset after the active team", got[1])
	}
}

func TestCompletionListsTheTasksACommandCanWorkOn(t *testing.T) {
	completionFixture(t)
	got := complete(t, "__complete", "team", "task", "done", "--team", "api", "")
	if len(got) != 1 || !strings.HasPrefix(got[0], "1\t") {
		t.Fatalf("done completions = %q, want only the open task #1", got)
	}
	if !strings.Contains(got[0], "handlers for /admin") || !strings.Contains(got[0], "lead") || !strings.Contains(got[0], "claimed") {
		t.Errorf("task description = %q, want the title, owner and status", got[0])
	}
}

func TestCompletionOffersModelsAfterAMemberTool(t *testing.T) {
	completionFixture(t)
	got := complete(t, "__complete", "team", "new", "x", "--member", "a=opencode:")
	if len(got) == 0 {
		t.Fatal("no models completed for a=opencode:")
	}
	for _, v := range got {
		if !strings.HasPrefix(v, "a=opencode:") || !strings.HasSuffix(v, "\topencode") {
			t.Fatalf("model completion = %q, want a=opencode:<model> with opencode", v)
		}
	}
}

func TestCompletionOffersRolesOfATeam(t *testing.T) {
	completionFixture(t)
	got := complete(t, "__complete", "team", "task", "add", "x", "--team", "api", "--owner", "")
	roles := map[string]bool{}
	for _, v := range got {
		role, _, _ := strings.Cut(v, "\t")
		roles[role] = true
	}
	if !roles["lead"] || !roles["backend"] {
		t.Errorf("--owner completions = %q, want lead and backend", got)
	}
}
