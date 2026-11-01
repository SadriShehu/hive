package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
)

// tasks returns a team's board, by number.
func (m Model) tasks(teamID string) []store.Task {
	var out []store.Task
	for _, t := range m.snap.Tasks {
		if t.TeamID == teamID {
			out = append(out, t)
		}
	}
	return out
}

// heldTitle is "#4 title" for the task a member works on, or "".
func (m Model) heldTitle(teamID, role string) string {
	if t, ok := team.Held(role, m.tasks(teamID)); ok {
		return fmt.Sprintf("#%d %s", t.N, cleanLine(t.Title))
	}
	return ""
}

// taskCount is "3/7 tasks" for a team's row, or "" without tasks.
func (m Model) taskCount(teamID string) string {
	c := team.Count(m.tasks(teamID))
	if c.Total == 0 {
		return ""
	}
	return fmt.Sprintf("%d/%d tasks", c.Done, c.Total)
}

// taskGlyph marks a task by its status.
func taskGlyph(status string) (string, lipgloss.Style) {
	switch status {
	case store.TaskDone:
		return "✓", sOK
	case store.TaskClaimed:
		return "●", lipgloss.NewStyle().Foreground(cWorking)
	case store.TaskBlocked:
		return "✗", sError
	}
	return "○", sDim
}

// taskLines lists a team's tasks for its preview in at most limit lines.
// When they don't fit, the done ones fold into one line, and then the last
// ones give way to a count.
func (m Model) taskLines(teamID string, w, limit int) []string {
	tasks := m.tasks(teamID)
	if len(tasks) == 0 || limit <= 0 {
		return nil
	}
	show, done := tasks, 0
	if len(tasks) > limit {
		show = nil
		for _, t := range tasks {
			if t.Status == store.TaskDone {
				done++
			} else {
				show = append(show, t)
			}
		}
	}
	var out []string
	if done > 0 {
		out = append(out, sOK.Render("✓")+sDim.Render(fmt.Sprintf(" %d done", done)))
	}
	more := 0
	if room := limit - len(out); len(show) > room {
		more = len(show) - max(0, room-1)
		show = show[:max(0, room-1)]
	}
	for _, t := range show {
		out = append(out, taskLine(t, tasks, w))
	}
	if more > 0 {
		out = append(out, sDim.Render(fmt.Sprintf("… %d more · hive team task ls", more)))
	}
	return out
}

// taskLine draws one task: mark, number, title, then what it waits for and
// its owner on the right.
func taskLine(t store.Task, tasks []store.Task, w int) string {
	g, gs := taskGlyph(t.Status)
	num := fmt.Sprintf("#%d ", t.N)
	right := t.Owner
	if waits := team.Waiting(t, tasks); len(waits) > 0 && !t.Closed() {
		right = strings.TrimSpace("after " + team.Numbers(waits) + "  " + right)
	}
	title := cleanLine(t.Title)
	if t.Status == store.TaskBlocked && t.Note != "" {
		title += " — " + cleanLine(t.Note)
	}
	titleStyle := sText
	if t.Status == store.TaskDone {
		titleStyle = sDim
	}
	tw := max(1, w-2-ansi.StringWidth(num)-ansi.StringWidth(right)-1)
	return gs.Render(g) + " " + sDim.Render(num) + titleStyle.Render(pad(ansi.Truncate(title, tw, "…"), tw)) +
		" " + sDim.Render(right)
}
