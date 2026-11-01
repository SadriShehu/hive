package team

import (
	"slices"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/store"
)

func TestParseTaskAndAfter(t *testing.T) {
	for in, want := range map[string]int{"4": 4, "#4": 4, " 12 ": 12} {
		if got, err := ParseTask(in); err != nil || got != want {
			t.Errorf("ParseTask(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0", "-1", "four", "#"} {
		if _, err := ParseTask(bad); err == nil {
			t.Errorf("ParseTask(%q) took it", bad)
		}
	}
	got, err := ParseAfter("1,#2, 3 1")
	if err != nil || !slices.Equal(got, []int{1, 2, 3}) {
		t.Errorf("ParseAfter = %v, %v; want [1 2 3], each once", got, err)
	}
	if got, err := ParseAfter(""); err != nil || got == nil || len(got) != 0 {
		t.Errorf("ParseAfter(\"\") = %#v, %v; want an empty list", got, err)
	}
	if _, err := ParseAfter("1,x"); err == nil {
		t.Error("ParseAfter took a word")
	}
}

// board is #1 done, #2 claimed by backend, #3 waiting for both, owned by
// frontend, and #4 waiting for #1 only.
func board() []store.Task {
	return []store.Task{
		{N: 1, Title: "schema", Status: store.TaskDone, Owner: "backend", After: []int{}},
		{N: 2, Title: "handlers", Status: store.TaskClaimed, Owner: "backend", After: []int{1}},
		{N: 3, Title: "table", Status: store.TaskOpen, Owner: "frontend", After: []int{1, 2}},
		{N: 4, Title: "docs", Status: store.TaskOpen, After: []int{1}},
	}
}

func TestReadiness(t *testing.T) {
	tasks := board()
	if w := Waiting(tasks[2], tasks); !slices.Equal(w, []int{2}) {
		t.Errorf("#3 waits for %v, want #2 only: #1 is done", w)
	}
	if Ready(tasks[2], tasks) || !Ready(tasks[3], tasks) || Ready(tasks[1], tasks) {
		t.Error("ready: want #4 only (#3 waits, #2 is claimed)")
	}
	if err := CheckAfter([]int{1, 4}, tasks); err != nil {
		t.Errorf("CheckAfter(1,4) = %v", err)
	}
	if err := CheckAfter([]int{9}, tasks); err == nil || !strings.Contains(err.Error(), "#9") {
		t.Errorf("CheckAfter(9) = %v, want #9 named", err)
	}
	if !Cycles(1, []int{4}, tasks) || !Cycles(2, []int{3}, tasks) {
		t.Error("a task that waits for the edited one was not seen as a cycle")
	}
	if Cycles(4, []int{3}, tasks) {
		t.Error("#3 doesn't wait for #4; that is not a cycle")
	}

	// #2 closes: #3 becomes ready; #4 was ready already and doesn't wait for #2.
	tasks[1].Status = store.TaskDone
	got := Unblocked(2, tasks)
	if len(got) != 1 || got[0].N != 3 {
		t.Errorf("Unblocked(2) = %+v, want #3", got)
	}
	// A blocked task isn't done: what waits for it waits on.
	tasks[1].Status = store.TaskBlocked
	if Ready(tasks[2], tasks) {
		t.Error("#3 is ready while #2 is blocked")
	}
}

func TestCanClaim(t *testing.T) {
	tasks := board()
	for _, c := range []struct {
		n     int
		role  string
		force bool
		want  string // "" for allowed, else part of the error
	}{
		{4, "frontend", false, ""},
		{3, "frontend", false, "waits for #2"},
		{3, "frontend", true, ""},
		{3, "backend", false, "frontend's"},
		{2, "frontend", false, "claimed by backend"},
		{2, "frontend", true, ""},
		{1, "backend", true, "done"},
	} {
		err := CanClaim(tasks[c.n-1], c.role, tasks, c.force)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s claiming #%d (force %v) = %v, want it allowed", c.role, c.n, c.force, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s claiming #%d (force %v) = %v, want %q", c.role, c.n, c.force, err, c.want)
		}
	}
}

func TestCountHeldAndEvents(t *testing.T) {
	tasks := board()
	if c := Count(tasks); c != (Counts{Total: 4, Open: 2, Claimed: 1, Done: 1}) {
		t.Errorf("Count = %+v", c)
	}
	if h, ok := Held("backend", tasks); !ok || h.N != 2 {
		t.Errorf("backend holds %+v, %v; want #2", h, ok)
	}
	if _, ok := Held("frontend", tasks); ok {
		t.Error("frontend holds a task it hasn't claimed")
	}
	for _, c := range []struct {
		task store.Task
		by   string
		want string
	}{
		{store.Task{N: 4, Title: "docs", Status: store.TaskDone, Note: "README updated"}, "backend", "#4 done by backend: README updated"},
		{store.Task{N: 4, Title: "docs", Status: store.TaskDone}, "", "#4 done: docs"},
		{store.Task{N: 4, Title: "docs", Status: store.TaskBlocked, Note: "no access"}, "lead", "#4 blocked by lead: no access"},
		{store.Task{N: 4, Title: "docs", Status: store.TaskClaimed}, "lead", "#4 claimed by lead: docs"},
		{store.Task{N: 4, Title: "docs", Status: store.TaskOpen, Owner: "lead"}, "you", "#4 added by you for lead: docs"},
	} {
		if got := TaskEvent(c.task, c.by); got != c.want {
			t.Errorf("TaskEvent = %q, want %q", got, c.want)
		}
	}
	if got := ReadyList(tasks[2:]); got != "#3 (frontend), #4" {
		t.Errorf("ReadyList = %q", got)
	}
}

func TestBriefTeachesTheBoard(t *testing.T) {
	members := []store.Member{{Role: "lead", Tool: "claude"}, {Role: "backend", Tool: "codex"}}
	tm := store.Team{Name: "api", Goal: "ship", Cwd: "/work", LeadRole: "lead"}
	lead := Brief(tm, members[0], members)
	for _, want := range []string{"hive team task add", "--owner", "--after", "Review what is marked done"} {
		if !strings.Contains(lead, want) {
			t.Errorf("the lead's brief lacks %q:\n%s", want, lead)
		}
	}
	member := Brief(tm, members[1], members)
	for _, want := range []string{"hive team task claim", "hive team task done", "hive team task block"} {
		if !strings.Contains(member, want) {
			t.Errorf("a member's brief lacks %q:\n%s", want, member)
		}
	}
	if strings.Contains(member, "hive team task add") {
		t.Errorf("a member's brief tells it to plan the board:\n%s", member)
	}
}
