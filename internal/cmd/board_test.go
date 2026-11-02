package cmd

import (
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/store"
)

// boardFixture makes a team with one task whose title says "channel" and one
// channel post, so the two sections are easy to tell apart.
func boardFixture(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HIVE_HOME", dir)
	t.Setenv("HIVE_CONFIG", filepath.Join(dir, "config.toml"))
	st, err := store.Open(filepath.Join(dir, "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tm := store.Team{ID: "t1", Name: "api", Goal: "ship the channel feature", Cwd: "/work", Status: store.TeamActive, CreatedAt: 1, UpdatedAt: 1}
	if err := st.CreateTeam(tm); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(store.Member{TeamID: tm.ID, Role: "lead", Tool: "claude", JoinedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTask(store.Task{TeamID: tm.ID, Title: "channel the updates", Body: "details", Status: store.TaskOpen, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddMessage(store.Message{TeamID: tm.ID, From: "lead", Body: "a post about the channel", Kind: store.MsgPost, At: 1}, nil); err != nil {
		t.Fatal(err)
	}
}

func runBoard(t *testing.T, args ...string) string {
	t.Helper()
	root := newRootCmd()
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{"team", "board", "api"}, args...))
	var err error
	out := captureStdout(t, func() { err = root.Execute() })
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBoardTasksPrintsOnlyTheTasks(t *testing.T) {
	boardFixture(t)
	out := runBoard(t, "--tasks")
	if !strings.Contains(out, "channel the updates") {
		t.Errorf("board --tasks = %q, want the task", out)
	}
	if strings.Contains(out, "a post about the channel") || strings.Contains(out, "goal:") || strings.Contains(out, "\nmembers") {
		t.Errorf("board --tasks = %q, want no channel or roster", out)
	}
}

func TestBoardChannelPrintsOnlyTheChannel(t *testing.T) {
	boardFixture(t)
	out := runBoard(t, "--channel")
	if !strings.Contains(out, "a post about the channel") {
		t.Errorf("board --channel = %q, want the post", out)
	}
	if strings.Contains(out, "channel the updates") || strings.Contains(out, "goal:") || strings.Contains(out, "\nmembers") {
		t.Errorf("board --channel = %q, want no tasks or roster", out)
	}
}

func TestBoardBothSectionsWithoutTheGoal(t *testing.T) {
	boardFixture(t)
	out := runBoard(t, "--tasks", "--channel")
	for _, want := range []string{"channel the updates", "a post about the channel"} {
		if !strings.Contains(out, want) {
			t.Errorf("board --tasks --channel = %q, want %q", out, want)
		}
	}
	if strings.Contains(out, "goal:") || strings.Contains(out, "\nmembers") {
		t.Errorf("board --tasks --channel = %q, want no goal or roster", out)
	}
}

func boardJSON(t *testing.T, args ...string) map[string]json.RawMessage {
	t.Helper()
	out := runBoard(t, append([]string{"--json"}, args...)...)
	var row map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &row); err != nil {
		t.Fatalf("JSON %q: %v", out, err)
	}
	return row
}

func TestBoardJSONTasksKeepsOnlyTasks(t *testing.T) {
	boardFixture(t)
	row := boardJSON(t, "--tasks")
	if len(row) != 1 {
		t.Errorf("JSON keys = %v, want only tasks", keys(row))
	}
	if _, ok := row["tasks"]; !ok {
		t.Errorf("JSON keys = %v, want tasks", keys(row))
	}
}

func TestBoardJSONChannelKeepsOnlyMessages(t *testing.T) {
	boardFixture(t)
	row := boardJSON(t, "--channel")
	if len(row) != 1 {
		t.Errorf("JSON keys = %v, want only messages", keys(row))
	}
	if _, ok := row["messages"]; !ok {
		t.Errorf("JSON keys = %v, want messages", keys(row))
	}
}

func keys(m map[string]json.RawMessage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
