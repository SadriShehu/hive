package cmd

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/store"
)

// planFixture makes a team with three roles and one task already on the board.
func planFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HIVE_HOME", dir)
	t.Setenv("HIVE_CONFIG", filepath.Join(dir, "config.toml"))
	st, err := store.Open(filepath.Join(dir, "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tm := store.Team{ID: "t1", Name: "api", Goal: "ship it", Cwd: "/work", Status: store.TeamActive, CreatedAt: 1, UpdatedAt: 1}
	if err := st.CreateTeam(tm); err != nil {
		t.Fatal(err)
	}
	for i, role := range []string{"lead", "manager", "uiux"} {
		if err := st.AddMember(store.Member{TeamID: tm.ID, Role: role, Tool: "claude", JoinedAt: int64(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.AddTask(store.Task{TeamID: tm.ID, Title: "old task", Status: store.TaskOpen, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writePlan(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runTaskAdd(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	root.SetErr(io.Discard)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"team", "task", "add", "--team", "api"}, args...))
	var err error
	out := captureStdout(t, func() { err = root.Execute() })
	return out, err
}

func planTasks(t *testing.T, dir string) []store.Task {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tasks, err := st.Tasks("t1")
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

func TestTaskAddFileResolvesRefsToTheNumbersJustGiven(t *testing.T) {
	dir := planFixture(t)
	plan := `[[task]]
ref = "inventory"
title = "List the features"
owner = "manager"

[[task]]
title = "Write the report"
owner = "manager"
after = ["inventory"]

[[task]]
title = "Review"
owner = "lead"
after = ["inventory", 1]
`
	out, err := runTaskAdd(t, "", "--file", writePlan(t, plan))
	if err != nil {
		t.Fatalf("add --file: %v", err)
	}
	for _, want := range []string{"#2 added for manager", "#3 added for manager · waits for #2", "#4 added for lead · waits for #2 #1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want %q", out, want)
		}
	}
	tasks := planTasks(t, dir)
	if len(tasks) != 4 {
		t.Fatalf("tasks = %d, want the old one and three more", len(tasks))
	}
	byN := map[int]store.Task{}
	for _, task := range tasks {
		byN[task.N] = task
	}
	if byN[2].Title != "List the features" || len(byN[2].After) != 0 {
		t.Errorf("#2 = %+v, want no waits", byN[2])
	}
	if !slices.Equal(byN[3].After, []int{2}) {
		t.Errorf("#3 after = %v, want [2]", byN[3].After)
	}
	if !slices.Equal(byN[4].After, []int{2, 1}) {
		t.Errorf("#4 after = %v, want [2 1]", byN[4].After)
	}
}

func TestTaskAddFileRefusesABadOwnerAndAddsNothing(t *testing.T) {
	dir := planFixture(t)
	plan := "[[task]]\ntitle = \"Mistake\"\nowner = \"manger\"\n"
	_, err := runTaskAdd(t, "", "--file", writePlan(t, plan))
	if err == nil || !strings.Contains(err.Error(), `task 1 "Mistake": owner "manger" is not on the team (roles: lead, manager, uiux)`) {
		t.Fatalf("error = %v, want the bad owner named with the roles", err)
	}
	if tasks := planTasks(t, dir); len(tasks) != 1 {
		t.Errorf("tasks = %d, want only the old one", len(tasks))
	}
}

func TestTaskAddFileRefusesARefToALaterTask(t *testing.T) {
	dir := planFixture(t)
	plan := `[[task]]
title = "First"
after = ["later"]

[[task]]
ref = "later"
title = "Second"
`
	_, err := runTaskAdd(t, "", "--file", writePlan(t, plan))
	if err == nil || !strings.Contains(err.Error(), `task 1 "First": waits for "later", which is not an earlier task in the file`) {
		t.Fatalf("error = %v, want the later ref refused", err)
	}
	if tasks := planTasks(t, dir); len(tasks) != 1 {
		t.Errorf("tasks = %d, want only the old one", len(tasks))
	}
}

func TestTaskAddFileRefusesADuplicateRefAndUnknownKeys(t *testing.T) {
	dir := planFixture(t)
	plan := `[[task]]
ref = "same"
title = "One"

[[task]]
ref = "same"
title = "Two"

[[task]]
title = "Three"
manger = "typo"
`
	_, err := runTaskAdd(t, "", "--file", writePlan(t, plan))
	if err == nil {
		t.Fatal("a duplicate ref and an unknown key were accepted")
	}
	for _, want := range []string{`ref "same" is already used by task 1`, `task 3 "Three": unknown key "manger"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want %q", err, want)
		}
	}
	if tasks := planTasks(t, dir); len(tasks) != 1 {
		t.Errorf("tasks = %d, want only the old one", len(tasks))
	}
}

func TestTaskAddFileDryRunAddsNothing(t *testing.T) {
	dir := planFixture(t)
	plan := `[[task]]
ref = "one"
title = "One"

[[task]]
title = "Two"
owner = "manager"
after = ["one"]
`
	out, err := runTaskAdd(t, "", "--file", writePlan(t, plan), "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	for _, want := range []string{"#2 added", "#3 added for", "waits for #2"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run = %q, want %q", out, want)
		}
	}
	if tasks := planTasks(t, dir); len(tasks) != 1 {
		t.Errorf("dry run added tasks: %d", len(tasks))
	}
}

func TestTaskAddFileReadsStdin(t *testing.T) {
	dir := planFixture(t)
	out, err := runTaskAdd(t, "[[task]]\ntitle = \"From stdin\"\n", "--file", "-")
	if err != nil {
		t.Fatalf("stdin plan: %v", err)
	}
	if !strings.Contains(out, "#2 added") {
		t.Errorf("output = %q, want the task added", out)
	}
	if tasks := planTasks(t, dir); len(tasks) != 2 || tasks[1].Title != "From stdin" {
		t.Errorf("tasks = %+v, want the stdin task", tasks)
	}
}
