package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/store"
)

// teamTasks is the api-v2 board: the schema done, backend on the handlers,
// the table waiting for them, and the frontend blocked on its own.
func teamTasks() []store.Task {
	return []store.Task{
		{TeamID: "t1", N: 1, Title: "schema for admin users", Status: store.TaskDone, Owner: "backend", After: []int{}},
		{TeamID: "t1", N: 2, Title: "handlers for /admin/users", Status: store.TaskClaimed, Owner: "backend", After: []int{1}},
		{TeamID: "t1", N: 3, Title: "admin table view", Status: store.TaskOpen, Owner: "frontend", After: []int{2}},
		{TeamID: "t1", N: 4, Title: "design review", Status: store.TaskBlocked, Owner: "frontend", Note: "no mockups", After: []int{}},
	}
}

func TestTheTreeShowsTheBoard(t *testing.T) {
	m, ops := setupTeam(t)
	ops.snap.Tasks = teamTasks()
	m = step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})
	m = step(t, m, feedMsg{teamID: "t1", msgs: ops.feed})
	view := ansi.Strip(m.View())
	for _, want := range []string{
		"3 members · 1/4 tasks",           // the team's row
		"backend · #2 handlers for",       // a member's row: the task it holds
		"lead · planning the API",         // one that holds none keeps its title
		"✓ #1 schema for admin users",     // the preview lists the board
		"● #2 handlers for /admin/users",  //
		"○ #3 admin table view",           //
		"after #2  frontend",              // what the table waits for, and whose it is
		"✗ #4 design review — no mockups", // why it is blocked
		"start with the schema",           // and the channel goes on below
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
}

func TestALongBoardFoldsItsDoneTasks(t *testing.T) {
	m, ops := setupTeam(t)
	var tasks []store.Task
	for n := 1; n <= 20; n++ {
		status := store.TaskDone
		if n > 8 {
			status = store.TaskOpen
		}
		tasks = append(tasks, store.Task{TeamID: "t1", N: n, Title: fmt.Sprintf("step %d", n), Status: status, After: []int{}})
	}
	ops.snap.Tasks = tasks
	m = step(t, m, tea.WindowSizeMsg{Width: 160, Height: 24})
	m = step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})
	m = step(t, m, feedMsg{teamID: "t1", msgs: ops.feed})
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "✓ 8 done") || strings.Contains(view, "#3 step 3") {
		t.Errorf("the done tasks aren't folded:\n%s", view)
	}
	if !strings.Contains(view, "more · hive team task ls") || !strings.Contains(view, "#9 step 9") || strings.Contains(view, "#20 step 20") {
		t.Errorf("want the first open tasks and a count of the rest:\n%s", view)
	}
	if !strings.Contains(view, "the list endpoint is GET") {
		t.Errorf("the board crowds out the channel:\n%s", view)
	}
}
