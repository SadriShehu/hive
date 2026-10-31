package store

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestTasksAreNumberedPerTeam(t *testing.T) {
	st := teamStore(t)
	must(t, st.CreateTeam(Team{ID: "t2", Name: "web", Cwd: "/web", CreatedAt: 1, UpdatedAt: 1}))

	for i, title := range []string{"schema", "handlers"} {
		task, err := st.AddTask(Task{TeamID: "t1", Title: title, Owner: "backend", After: []int{}, CreatedBy: "lead",
			CreatedAt: 10, UpdatedAt: 10})
		if err != nil || task.N != i+1 {
			t.Fatalf("AddTask(%s) = %+v, %v; want #%d", title, task, err, i+1)
		}
	}
	if task, err := st.AddTask(Task{TeamID: "t2", Title: "page", CreatedAt: 11, UpdatedAt: 11}); err != nil || task.N != 1 {
		t.Fatalf("the other team's first task = %+v, %v; want #1", task, err)
	}
	third, err := st.AddTask(Task{TeamID: "t1", Title: "table", After: []int{1, 2}, CreatedAt: 12, UpdatedAt: 12})
	if err != nil || third.N != 3 {
		t.Fatalf("AddTask = %+v, %v; want #3", third, err)
	}

	tasks, err := st.Tasks("t1")
	if err != nil || len(tasks) != 3 {
		t.Fatalf("Tasks = %+v, %v", tasks, err)
	}
	if got := tasks[2]; got.Status != TaskOpen || !slices.Equal(got.After, []int{1, 2}) {
		t.Errorf("#3 = %+v, want it open, waiting for #1 and #2", got)
	}
	if tasks[0].After == nil {
		t.Error("a task that waits for nothing reads back After as nil, which prints as null")
	}
	if tm, _ := st.TeamByID("t1"); tm.UpdatedAt != 12 {
		t.Errorf("the team's updated_at = %d, want the last task's 12", tm.UpdatedAt)
	}
	if _, err := st.Task("t1", 9); err == nil {
		t.Error("Task found #9")
	}
	if all, _ := st.AllTasks(); len(all) != 4 {
		t.Errorf("AllTasks = %d tasks, want 4", len(all))
	}

	must(t, st.DeleteTeam("t1"))
	if tasks, _ := st.Tasks("t1"); len(tasks) != 0 {
		t.Errorf("a deleted team keeps its tasks: %+v", tasks)
	}
}

func TestUpdateTaskLosesToAnEarlierMove(t *testing.T) {
	st := teamStore(t)
	open, err := st.AddTask(Task{TeamID: "t1", Title: "schema", CreatedAt: 10, UpdatedAt: 10})
	must(t, err)

	// lead and backend both read #1 open, and claim it.
	byLead, byBackend := open, open
	byLead.Status, byLead.Owner, byLead.UpdatedAt = TaskClaimed, "lead", 20
	byBackend.Status, byBackend.Owner, byBackend.UpdatedAt = TaskClaimed, "backend", 21
	if ok, err := st.UpdateTask(open, byLead); err != nil || !ok {
		t.Fatalf("the first claim = %v, %v", ok, err)
	}
	if ok, err := st.UpdateTask(open, byBackend); err != nil || ok {
		t.Fatalf("the second claim = %v, %v; want it refused", ok, err)
	}
	if got, _ := st.Task("t1", 1); got.Owner != "lead" || got.Status != TaskClaimed {
		t.Errorf("#1 = %+v, want lead's", got)
	}
}

func TestClaimMessagesTakesOnlyWhatWaits(t *testing.T) {
	st := teamStore(t)
	a, _ := st.AddMessage(Message{TeamID: "t1", From: "lead", To: "backend", Body: "a", Kind: MsgSay, At: 1}, []string{"backend"})
	b, _ := st.AddMessage(Message{TeamID: "t1", From: "lead", To: "backend", Body: "b", Kind: MsgSay, At: 2}, []string{"backend"})
	if _, err := st.ClaimPending("t1", "backend", 3); err != nil {
		t.Fatal(err)
	}
	must(t, st.Unclaim("backend", []Message{b}))

	got, err := st.ClaimMessages("backend", []Message{a, b}, 4)
	if err != nil || len(got) != 1 || got[0].Body != "b" {
		t.Fatalf("ClaimMessages = %+v, %v; want b only: a was delivered", got, err)
	}
	if after, _ := st.MessagesAfter("t1", a.ID); len(after) != 1 || after[0].ID != b.ID {
		t.Errorf("MessagesAfter = %+v, want b", after)
	}
	if last, _ := st.LastMessageID("t1"); last != b.ID {
		t.Errorf("LastMessageID = %d, want %d", last, b.ID)
	}
}

// The board's migration can run twice, as an older hive makes it do.
func TestTasksSurviveAnOlderHive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hive.db")
	st, err := Open(path)
	must(t, err)
	must(t, st.CreateTeam(Team{ID: "t1", Name: "api", Cwd: "/work", CreatedAt: 1, UpdatedAt: 1}))
	_, err = st.AddTask(Task{TeamID: "t1", Title: "schema", CreatedAt: 1, UpdatedAt: 1})
	must(t, err)
	_, err = st.db.Exec(`PRAGMA user_version = 2`)
	must(t, err)
	st.Close()

	if st, err = Open(path); err != nil {
		t.Fatalf("reopening after an older hive: %v", err)
	}
	defer st.Close()
	if tasks, _ := st.Tasks("t1"); len(tasks) != 1 {
		t.Errorf("tasks = %+v, want #1 kept", tasks)
	}
}
