package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
)

// The task form's fields, in tab order.
const (
	taskFieldTitle = iota
	taskFieldBody
	taskFieldOwner
	taskFieldAfter
)

type taskFormKind int

const (
	taskFormAdd taskFormKind = iota
	taskFormEdit
	taskFormDone
	taskFormBlock
)

// taskEditor is a team's board, open full screen: the tasks in it and, while
// one is being added or changed, the form.
type taskEditor struct {
	team   string // the team whose board this is
	cursor int    // into the team's tasks
	selN   int    // the task the cursor is on, kept across a refresh
	form   *taskForm
}

// taskForm edits one task, or adds one: title, body, owner and after; for
// done and block, just the note. Every field wraps rather than scrolling its
// text out of view.
type taskForm struct {
	kind  taskFormKind
	n     int // the task edited, done or blocked
	field int
	title textarea.Model
	body  textarea.Model
	owner textarea.Model
	after textarea.Model
	note  textarea.Model
}

func newTaskForm(kind taskFormKind, n, w int) *taskForm {
	f := &taskForm{kind: kind, n: n}
	f.title = newPromptArea("what needs doing")
	f.body = newPromptArea("details (optional)")
	f.owner = newPromptArea("a role, or blank")
	f.after = newPromptArea("1,2; blank for none")
	f.note = newPromptArea("what changed (optional)")
	if kind == taskFormBlock {
		f.note.Placeholder = "why it is blocked"
	}
	f.layout(w)
	return f
}

func (f *taskForm) layout(w int) {
	f.title.SetWidth(max(10, w-12))
	f.owner.SetWidth(max(10, w-12))
	f.after.SetWidth(max(10, w-12))
	f.body.SetWidth(max(10, w-12))
	f.note.SetWidth(max(10, w-12))
}

// fields is how many rows tab moves between.
func (f *taskForm) fields() int {
	if f.kind == taskFormDone || f.kind == taskFormBlock {
		return 1
	}
	return taskFieldAfter + 1
}

func (f *taskForm) focus(field int) tea.Cmd {
	f.field = field
	f.title.Blur()
	f.body.Blur()
	f.owner.Blur()
	f.after.Blur()
	f.note.Blur()
	if f.kind == taskFormDone || f.kind == taskFormBlock {
		return f.note.Focus()
	}
	switch field {
	case taskFieldTitle:
		return f.title.Focus()
	case taskFieldBody:
		return f.body.Focus()
	case taskFieldOwner:
		return f.owner.Focus()
	case taskFieldAfter:
		return f.after.Focus()
	}
	return nil
}

// area is the field the form is on, which takes the caret keys.
func (f *taskForm) area() *textarea.Model {
	if f.kind == taskFormDone || f.kind == taskFormBlock {
		return &f.note
	}
	switch f.field {
	case taskFieldTitle:
		return &f.title
	case taskFieldBody:
		return &f.body
	case taskFieldOwner:
		return &f.owner
	case taskFieldAfter:
		return &f.after
	}
	return &f.note
}

func (f *taskForm) update(msg tea.KeyMsg) tea.Cmd {
	var cmd tea.Cmd
	if f.kind == taskFormDone || f.kind == taskFormBlock {
		f.note, cmd = f.note.Update(msg)
		return cmd
	}
	switch f.field {
	case taskFieldTitle:
		f.title, cmd = f.title.Update(msg)
	case taskFieldBody:
		f.body, cmd = f.body.Update(msg)
	case taskFieldOwner:
		f.owner, cmd = f.owner.Update(msg)
	case taskFieldAfter:
		f.after, cmd = f.after.Update(msg)
	}
	return cmd
}

// openTaskEditor shows a team's board.
func (m Model) openTaskEditor(tm store.Team) (Model, tea.Cmd) {
	tasks := m.tasks(tm.ID)
	ed := taskEditor{team: tm.ID}
	if len(tasks) > 0 {
		ed.selN = tasks[0].N
	}
	m.taskEdit = ed
	m.mode = modeTasks
	return m, nil
}

// syncTaskEditor keeps the cursor on the same task after a refresh, or on
// the nearest one when it is gone.
func (m *Model) syncTaskEditor() {
	if m.mode != modeTasks {
		return
	}
	tasks := m.tasks(m.taskEdit.team)
	if len(tasks) == 0 {
		m.taskEdit.cursor, m.taskEdit.selN = 0, 0
		return
	}
	at := -1
	if m.taskEdit.selN > 0 {
		for i, task := range tasks {
			if task.N == m.taskEdit.selN {
				at = i
				break
			}
		}
	}
	if at < 0 {
		at = min(m.taskEdit.cursor, len(tasks)-1)
	}
	m.taskEdit.cursor = max(0, at)
	m.taskEdit.selN = tasks[m.taskEdit.cursor].N
}

// selectedTask is the task under the editor's cursor.
func (m Model) selectedTask() (store.Task, bool) {
	tasks := m.tasks(m.taskEdit.team)
	if m.taskEdit.cursor < 0 || m.taskEdit.cursor >= len(tasks) {
		return store.Task{}, false
	}
	return tasks[m.taskEdit.cursor], true
}

func (m Model) keyTasks(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.taskEdit.form != nil {
		return m.keyTaskForm(msg)
	}
	tasks := m.tasks(m.taskEdit.team)
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		return m, nil
	case "up", "k":
		return m.moveTaskCursor(tasks, -1)
	case "down", "j":
		return m.moveTaskCursor(tasks, 1)
	case "home", "g":
		return m.moveTaskCursor(tasks, -len(tasks))
	case "end", "G":
		return m.moveTaskCursor(tasks, len(tasks))
	case "n":
		return m, m.openTaskForm(taskFormAdd)
	case "e", "enter":
		return m, m.openTaskForm(taskFormEdit)
	case "d":
		return m, m.openTaskForm(taskFormDone)
	case "b":
		return m, m.openTaskForm(taskFormBlock)
	}
	return m, nil
}

func (m Model) moveTaskCursor(tasks []store.Task, delta int) (tea.Model, tea.Cmd) {
	if len(tasks) == 0 {
		return m, nil
	}
	m.taskEdit.cursor = max(0, min(len(tasks)-1, m.taskEdit.cursor+delta))
	m.taskEdit.selN = tasks[m.taskEdit.cursor].N
	return m, nil
}

// openTaskForm shows a form for a new task or the selected one. Adding needs
// no selection; the rest say what to do when the board is empty.
func (m *Model) openTaskForm(kind taskFormKind) tea.Cmd {
	f := newTaskForm(kind, 0, m.width)
	if kind != taskFormAdd {
		t, ok := m.selectedTask()
		if !ok {
			m.setFlash("the board is empty; n adds a task", true)
			return nil
		}
		f.n = t.N
		if kind == taskFormEdit {
			f.title.SetValue(t.Title)
			f.title.CursorEnd()
			f.body.SetValue(t.Body)
			f.owner.SetValue(t.Owner)
			f.owner.CursorEnd()
			f.after.SetValue(team.Numbers(t.After))
			f.after.CursorEnd()
		}
	}
	m.taskEdit.form = f
	return f.focus(0)
}

func (m Model) keyTaskForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.taskEdit.form
	switch msg.String() {
	case "esc":
		m.taskEdit.form = nil
		return m, nil
	case "tab":
		return m, f.focus((f.field + 1) % f.fields())
	case "shift+tab":
		return m, f.focus((f.field + f.fields() - 1) % f.fields())
	case "up", "down":
		// Inside a field that has text, up and down move the caret through
		// its wrapped lines; tab moves between fields.
		if f.area().Value() == "" {
			step := 1
			if msg.String() == "up" {
				step = -1
			}
			return m, f.focus((f.field + step + f.fields()) % f.fields())
		}
	case "enter":
		if f.field < f.fields()-1 {
			return m, f.focus(f.field + 1)
		}
		return m.submitTaskForm()
	}
	return m, f.update(msg)
}

// submitTaskForm checks what the form can check here, and asks Ops for the
// rest. The list stays open for the refresh, which shows the change.
func (m Model) submitTaskForm() (tea.Model, tea.Cmd) {
	f := m.taskEdit.form
	ops, teamID := m.ops, m.taskEdit.team
	tm, _ := m.teamByID(teamID)
	name := tm.Name
	ed := &m.taskEdit
	switch f.kind {
	case taskFormDone:
		n, note := f.n, strings.TrimSpace(f.note.Value())
		ed.form, ed.selN = nil, n
		return m, func() tea.Msg {
			_, err := ops.TeamTaskDone(teamID, n, note)
			return doneMsg{text: fmt.Sprintf("#%d marked done on %s", n, name), err: err, task: n}
		}
	case taskFormBlock:
		n, why := f.n, strings.TrimSpace(f.note.Value())
		if why == "" {
			m.setFlash("say why it is blocked", true)
			return m, nil
		}
		ed.form, ed.selN = nil, n
		return m, func() tea.Msg {
			_, err := ops.TeamTaskBlock(teamID, n, why)
			return doneMsg{text: fmt.Sprintf("#%d blocked on %s", n, name), err: err, task: n}
		}
	}
	title := strings.TrimSpace(f.title.Value())
	if title == "" {
		m.setFlash("a task needs a title", true)
		return m, nil
	}
	after, err := team.ParseAfter(f.after.Value())
	if err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	body, owner := strings.TrimSpace(f.body.Value()), strings.TrimSpace(f.owner.Value())
	if f.kind == taskFormAdd {
		ed.cursor, ed.selN = len(m.tasks(teamID)), 0
		ed.form = nil
		return m, func() tea.Msg {
			task, err := ops.TeamTaskAdd(teamID, title, body, owner, after)
			return doneMsg{text: fmt.Sprintf("added #%d to %s", task.N, name), err: err, task: task.N}
		}
	}
	n := f.n
	ed.form, ed.selN = nil, n
	return m, func() tea.Msg {
		_, err := ops.TeamTaskEdit(teamID, n, title, body, owner, after)
		return doneMsg{text: fmt.Sprintf("edited #%d on %s", n, name), err: err, task: n}
	}
}

// taskEditorLines draws the board editor: the team, its tasks with a cursor,
// and the selected task's details.
func (m Model) taskEditorLines(w, h int) []string {
	if m.taskEdit.form != nil {
		return m.taskFormLines(w, h)
	}
	tm, ok := m.teamByID(m.taskEdit.team)
	if !ok {
		return fit([]string{sDim.Render("this team is gone; esc goes back to the tree")}, w, h)
	}
	tasks := m.tasks(tm.ID)
	c := team.Count(tasks)
	head := []string{
		sAccent.Bold(true).Render("⬡ "+tm.Name) + sDim.Render(" · tasks"),
		sDim.Render(ansi.Truncate(fmt.Sprintf("%d tasks · %d done · %d open · %d claimed · %d blocked",
			c.Total, c.Done, c.Open, c.Claimed, c.Blocked), w, "…")),
		sRule.Render(strings.Repeat("─", max(1, w))),
	}
	if len(tasks) == 0 {
		return fit(append(head, "", sDim.Render(" no tasks yet; n adds one")), w, h)
	}
	listRows := min(len(tasks), max(3, (h-len(head)-2)/2))
	listRows = max(1, min(listRows, h-len(head)-3))
	start := min(max(0, m.taskEdit.cursor-listRows+1), len(tasks)-listRows)
	out := append([]string{}, head...)
	for i := start; i < start+listRows; i++ {
		out = append(out, taskEditorRow(tasks[i], tasks, w, i == m.taskEdit.cursor))
	}
	out = append(out, sRule.Render(strings.Repeat("─", max(1, w))))
	t, ok := m.selectedTask()
	if !ok {
		t = tasks[max(0, min(m.taskEdit.cursor, len(tasks)-1))]
	}
	detail := taskDetail(t, tasks, w)
	if room := h - len(out); len(detail) > room {
		detail = detail[:max(0, room)]
	}
	return fit(append(out, detail...), w, h)
}

// taskEditorRow draws one task: its status, number and title, then its status
// word, owner and what it waits for.
func taskEditorRow(t store.Task, tasks []store.Task, w int, sel bool) string {
	g, gs := taskGlyph(t.Status)
	mark := "  "
	if sel {
		mark = sAccent.Render("› ")
	}
	num := fmt.Sprintf("#%d", t.N)
	right := t.Status
	if t.Owner != "" {
		right += " · " + t.Owner
	}
	if waits := team.Waiting(t, tasks); len(waits) > 0 && !t.Closed() {
		right += " · after " + team.Numbers(waits)
	}
	right = ansi.Truncate(right, max(12, w/2), "…")
	title := cleanLine(t.Title)
	titleStyle := sText
	if t.Status == store.TaskDone {
		titleStyle = sDim
	}
	if sel {
		titleStyle = titleStyle.Bold(true)
	}
	prefix := mark + gs.Render(g) + " " + sDim.Render(num) + " "
	tw := w - ansi.StringWidth(prefix) - ansi.StringWidth(right) - 1
	if tw >= 8 {
		return prefix + titleStyle.Render(pad(ansi.Truncate(title, tw, "…"), tw)) + " " + sDim.Render(right)
	}
	return prefix + titleStyle.Render(ansi.Truncate(title, max(1, w-ansi.StringWidth(prefix)), "…"))
}

// taskDetail says what the selected task is: its status, owner, what it
// waits for, its body and its note.
func taskDetail(t store.Task, tasks []store.Task, w int) []string {
	g, gs := taskGlyph(t.Status)
	// The title wraps under its number, so a long one isn't cut in the
	// detail the list row points to.
	out := []string{}
	for i, line := range strings.Split(ansi.Wrap(fmt.Sprintf("#%d %s", t.N, cleanLine(t.Title)), max(10, w-2), ""), "\n") {
		prefix := "  "
		if i == 0 {
			prefix = gs.Render(g) + " "
		}
		out = append(out, prefix+sBold.Render(line))
	}
	meta := "  " + sDim.Render(t.Status)
	if t.Owner != "" {
		meta += sDim.Render(" · " + t.Owner)
	}
	if len(t.After) > 0 {
		meta += sDim.Render(" · after " + team.Numbers(t.After))
	}
	out = append(out, ansi.Truncate(meta, w, "…"))
	wrap := func(label, body string) {
		out = append(out, "  "+sDim.Render(label))
		for _, l := range strings.Split(ansi.Wrap(team.Clean(body), max(10, w-4), ""), "\n") {
			out = append(out, "  "+sText.Render(l))
		}
	}
	if t.Body != "" {
		wrap("body", t.Body)
	}
	if t.Note != "" {
		label := "note"
		if t.Status == store.TaskBlocked {
			label = "blocked"
		}
		wrap(label, t.Note)
	}
	return out
}

func (m Model) taskFormLines(w, h int) []string {
	f := m.taskEdit.form
	tm, _ := m.teamByID(m.taskEdit.team)
	heading := "New task"
	switch f.kind {
	case taskFormEdit:
		heading = fmt.Sprintf("Edit #%d", f.n)
	case taskFormDone:
		heading = fmt.Sprintf("Done #%d", f.n)
	case taskFormBlock:
		heading = fmt.Sprintf("Block #%d", f.n)
	}
	if tm.Name != "" {
		heading += sDim.Render(" · " + tm.Name)
	}
	mark := func(i int) string {
		if f.field == i {
			return sAccent.Render("› ")
		}
		return "  "
	}
	out := []string{sBold.Render(heading), ""}
	if f.kind == taskFormDone || f.kind == taskFormBlock {
		label := map[taskFormKind]string{taskFormDone: "note", taskFormBlock: "why"}[f.kind]
		out = append(out, fieldLines(mark(0), label, f.note, 8, w, max(1, h-formChromeRows-1))...)
		out = append(out, "", sDim.Render("hive posts it to the channel, and to the lead when an agent leads."))
		return fit(out, w, h)
	}
	// Each field grows a few rows to show its text; the body takes what is
	// left, so the form keeps the screen's height.
	titleRows := fieldRows(f.title, 5)
	ownerRows := fieldRows(f.owner, 2)
	afterRows := fieldRows(f.after, 2)
	bodyRows := max(1, min(6, h-4-titleRows-ownerRows-afterRows))
	out = append(out, fieldLines(mark(taskFieldTitle), "title", f.title, 8, w, titleRows)...)
	out = append(out, fieldLines(mark(taskFieldBody), "body", f.body, 8, w, bodyRows)...)
	out = append(out, fieldLines(mark(taskFieldOwner), "owner", f.owner, 8, w, ownerRows)...)
	out = append(out, fieldLines(mark(taskFieldAfter), "after", f.after, 8, w, afterRows)...)
	out = append(out,
		"",
		sDim.Render("the owner is a role on the team; after waits for tasks on the board, like 1,2."),
	)
	return fit(out, w, h)
}

// fieldRows is how many rows a wrapped form field draws, at most most.
func fieldRows(area textarea.Model, most int) int {
	return max(1, len(visibleRows(area, most)))
}

// fieldLines draws a form field whose text wraps: the label with the first
// line, the rest indented under it. It shows at most rows lines, around the
// cursor, cut to the screen's width.
func fieldLines(mark, label string, area textarea.Model, labelW, w, rows int) []string {
	indent := "  " + strings.Repeat(" ", labelW)
	var out []string
	for i, line := range visibleRows(area, rows) {
		prefix := indent
		if i == 0 {
			prefix = mark + sDim.Render(fmt.Sprintf("%-*s", labelW, label))
		}
		out = append(out, ansi.Truncate(prefix+line, w, ""))
	}
	return out
}
