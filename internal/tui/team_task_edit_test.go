package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func teamWithTasks(t *testing.T) (Model, *fakeOps) {
	t.Helper()
	m, ops := setupTeam(t)
	ops.snap.Tasks = teamTasks()
	return step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap}), ops
}

func TestEGivesATeamItsTaskEditor(t *testing.T) {
	m, _ := teamWithTasks(t)
	m = press(t, m, "e")
	if m.mode != modeTasks || m.taskEdit.team != "t1" {
		t.Fatalf("mode = %v, team = %q; want the task editor for t1", m.mode, m.taskEdit.team)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"api-v2 · tasks", "4 tasks · 1 done", "#1 schema for admin users",
		"#2 handlers for /admin/users", "claimed · backend", "after #2", "#3 admin table view"} {
		if !strings.Contains(view, want) {
			t.Errorf("editor lacks %q:\n%s", want, view)
		}
	}
	if m.taskEdit.selN != 1 {
		t.Fatalf("selected #%d, want #1", m.taskEdit.selN)
	}
	m = press(t, m, "j", "j", "j")
	if m.taskEdit.selN != 4 {
		t.Fatalf("selected #%d after j,j,j, want #4", m.taskEdit.selN)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "blocked") || !strings.Contains(view, "no mockups") {
		t.Errorf("the selected task's note isn't shown:\n%s", view)
	}
	m = press(t, m, "esc")
	if m.mode != modeNormal {
		t.Fatalf("mode = %v after esc, want the tree", m.mode)
	}
}

func TestTaskEditorAddsEditsClosesAndBlocks(t *testing.T) {
	m, ops := teamWithTasks(t)
	m = press(t, m, "e")

	m = press(t, m, "n")
	f := m.taskEdit.form
	if f == nil || f.kind != taskFormAdd || m.mode != modeTasks {
		t.Fatalf("n: form = %+v, mode = %v; want the add form over the editor", f, m.mode)
	}
	f.title.SetValue("write the docs")
	f.body.SetValue("cover the new endpoints")
	f.owner.SetValue("backend")
	f.after.SetValue("1, 2")
	f.focus(taskFieldAfter)
	m = press(t, m, "enter")
	if len(ops.taskAdded) != 1 {
		t.Fatalf("adds = %+v", ops.taskAdded)
	}
	if got := ops.taskAdded[0]; got.team != "t1" || got.title != "write the docs" || got.body != "cover the new endpoints" ||
		got.owner != "backend" || len(got.after) != 2 || got.after[0] != 1 || got.after[1] != 2 {
		t.Fatalf("add = %+v", got)
	}
	if m.mode != modeTasks || m.taskEdit.form != nil || m.taskEdit.selN != 5 {
		t.Fatalf("after add: mode %v, form %+v, selected #%d; want the new #5 in the editor",
			m.mode, m.taskEdit.form, m.taskEdit.selN)
	}

	m = press(t, m, "e")
	f = m.taskEdit.form
	if f == nil || f.kind != taskFormEdit || f.n != 5 || f.title.Value() != "write the docs" {
		t.Fatalf("e: form = %+v", f)
	}
	f.title.SetValue("write the docs well")
	f.body.SetValue("and the tests")
	f.owner.SetValue("frontend")
	f.after.SetValue("1")
	f.focus(taskFieldAfter)
	m = press(t, m, "enter")
	if len(ops.taskEdited) != 1 {
		t.Fatalf("edits = %+v", ops.taskEdited)
	}
	if got := ops.taskEdited[0]; got.team != "t1" || got.n != 5 || got.title != "write the docs well" ||
		got.body != "and the tests" || got.owner != "frontend" || len(got.after) != 1 || got.after[0] != 1 {
		t.Fatalf("edit = %+v", got)
	}

	m = press(t, m, "d")
	if m.taskEdit.form == nil || m.taskEdit.form.kind != taskFormDone {
		t.Fatalf("d: form = %+v, want the done form", m.taskEdit.form)
	}
	m.taskEdit.form.note.SetValue("shipped")
	m = press(t, m, "enter")
	if len(ops.taskDone) != 1 || ops.taskDone[0].n != 5 || ops.taskDone[0].note != "shipped" {
		t.Fatalf("done = %+v", ops.taskDone)
	}

	m = press(t, m, "g", "j", "j") // #3, still open
	m = press(t, m, "b")
	if m.taskEdit.form == nil || m.taskEdit.form.kind != taskFormBlock {
		t.Fatalf("b: form = %+v, want the block form", m.taskEdit.form)
	}
	m = press(t, m, "enter")
	if len(ops.taskBlocked) != 0 || m.taskEdit.form == nil {
		t.Fatalf("a block without why went through: %+v, form %+v", ops.taskBlocked, m.taskEdit.form)
	}
	if !strings.Contains(m.flash, "why") {
		t.Errorf("flash = %q, want the reason", m.flash)
	}
	m.taskEdit.form.note.SetValue("waiting on the API")
	m = press(t, m, "enter")
	if len(ops.taskBlocked) != 1 || ops.taskBlocked[0].n != 3 || ops.taskBlocked[0].note != "waiting on the API" {
		t.Fatalf("block = %+v", ops.taskBlocked)
	}
	if m.mode != modeTasks || m.taskEdit.form != nil {
		t.Fatalf("after block: mode %v, form %+v; want the list", m.mode, m.taskEdit.form)
	}
}

func TestTaskEditorOpensOnlyOnATeam(t *testing.T) {
	m, _ := setup(t, false)
	m = press(t, m, "e")
	if m.mode != modeNormal {
		t.Fatalf("e on an ordinary session opened mode %v", m.mode)
	}

	m, _ = setupTeam(t)
	m = press(t, m, "j", "e") // lead, a member row
	if m.mode != modeNormal {
		t.Fatalf("e on a member row opened mode %v", m.mode)
	}
}

func TestTaskEditorFitsANarrowWindow(t *testing.T) {
	m, _ := teamWithTasks(t)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m = press(t, m, "e")
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "api-v2 · tasks") || !strings.Contains(view, "#2 handlers") {
		t.Fatalf("narrow editor:\n%s", view)
	}
	if len(strings.Split(view, "\n")) != 20 {
		t.Errorf("narrow view has %d lines, want 20", len(strings.Split(view, "\n")))
	}
}
