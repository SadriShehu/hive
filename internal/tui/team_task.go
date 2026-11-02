package tui

import (
	"cmp"
	"fmt"
	"slices"
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
// When they don't fit, the block folds: a summary line of counts, then the
// claimed, blocked and open tasks by number, then the done ones, and a last
// line saying how many are left and that e opens the board.
func (m Model) taskLines(teamID string, w, limit int) []string {
	tasks := m.tasks(teamID)
	if len(tasks) == 0 || limit <= 0 {
		return nil
	}
	if len(tasks) <= limit {
		out := make([]string, 0, len(tasks))
		for _, t := range tasks {
			out = append(out, taskLine(t, tasks, w))
		}
		return out
	}
	out := []string{taskSummary(team.Count(tasks))}
	room := limit - 1
	if room <= 0 {
		return out
	}
	ordered := slices.Clone(tasks)
	slices.SortStableFunc(ordered, func(a, b store.Task) int {
		if r := taskRank(a.Status) - taskRank(b.Status); r != 0 {
			return r
		}
		return cmp.Compare(a.N, b.N)
	})
	keep := room
	if len(ordered) > room {
		keep = room - 1
	}
	show := ordered[:max(0, keep)]
	for _, t := range show {
		out = append(out, taskLine(t, tasks, w))
	}
	if more := len(ordered) - len(show); more > 0 {
		out = append(out, sDim.Render(fmt.Sprintf("… %d more · e opens the board", more)))
	}
	return out
}

// taskRank orders a folded board: what moves comes before what waits, and
// done comes last.
func taskRank(status string) int {
	switch status {
	case store.TaskClaimed:
		return 0
	case store.TaskBlocked:
		return 1
	case store.TaskOpen:
		return 2
	}
	return 3
}

// taskSummary is the folded board's first line: "1/6 done · 3 claimed · 2
// open", with the parts that are zero left out.
func taskSummary(c team.Counts) string {
	parts := []string{fmt.Sprintf("%d/%d done", c.Done, c.Total)}
	if c.Claimed > 0 {
		parts = append(parts, fmt.Sprintf("%d claimed", c.Claimed))
	}
	if c.Open > 0 {
		parts = append(parts, fmt.Sprintf("%d open", c.Open))
	}
	return sDim.Render(strings.Join(parts, " · "))
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
