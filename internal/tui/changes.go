package tui

import (
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/gitx"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tmux"
	"github.com/sadrishehu/hive/internal/tracker"
)

// changesEvery is how often the changes preview is read again while it is on.
const changesEvery = 5 * time.Second

// changesFetch is the changes preview's last read, for which row it was.
type changesFetch struct {
	id     string // the session, team and role it belongs to
	at     time.Time
	report tracker.ChangeReport
	err    string
}

type changesMsg struct {
	id     string
	report tracker.ChangeReport
	err    error
	at     time.Time
}

// changesID names the row a change report belongs to. A member's changes come
// from its worktree, so a team and role move the identity too.
func changesID(s store.Session, teamID, role string) string {
	return s.ID + "|" + teamID + "|" + role
}

// changesInView reports whether the changes preview is on screen and worth
// reading: there is a preview, and it is either beside the tree or taking the
// body on a narrow screen.
func (m Model) changesInView() bool {
	return m.showChanges && (m.previewVisible() || m.changesTakesTheBody())
}

// changesTakesTheBody is usageTakesTheBody for the changes preview.
func (m Model) changesTakesTheBody() bool {
	return m.showChanges && m.showPreview && !m.previewVisible() &&
		m.mode != modeNew && m.mode != modeHelp && m.mode != modeChannel && m.mode != modeTasks &&
		m.mode != modeTeam
}

// changesCmd reads the selected row's changes when the preview is on, at most
// every changesEvery. Git never runs in View.
func (m *Model) changesCmd() tea.Cmd {
	if !m.changesInView() {
		return nil
	}
	s, ok := m.selected()
	if !ok || s.Source == store.SourceTeam {
		return nil
	}
	n := m.selectedNode()
	teamID, role := "", ""
	if n != nil {
		teamID, role = n.TeamID, n.Role
	}
	id := changesID(s, teamID, role)
	now := m.now()
	if m.changes.id == id && now.Sub(m.changes.at) < changesEvery {
		return nil
	}
	m.changes = changesFetch{id: id, at: now, report: m.changes.report}
	ops := m.ops
	return func() tea.Msg {
		report, err := ops.Changes(s, teamID, role)
		return changesMsg{id: id, report: report, err: err, at: now}
	}
}

// popupDiff opens the full diff in a tmux popup, the way the tree popup is
// bound: the binary and the ID are quoted for the shell tmux runs.
func (m Model) popupDiff(s store.Session) tea.Cmd {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "hive"
	}
	command := tmux.ShellQuote(exe) + " diff " + tmux.ShellQuote(s.ID)
	ops := m.ops
	return func() tea.Msg {
		if err := ops.Popup(command); err != nil {
			return doneMsg{err: err}
		}
		return doneMsg{text: "opened the full diff"}
	}
}

// changesLines draws the changes preview: a label and D back, the summary, the
// files, then as much of the diff as fits.
func (m Model) changesLines(s store.Session, role string, w, h int) []string {
	if m.changes.id != changesID(s, m.nodeTeamID(), role) {
		return []string{sDim.Render("…")}
	}
	if m.changes.err != "" {
		return []string{sError.Render(ansi.Truncate("✗ "+cleanLine(m.changes.err), w, "…"))}
	}
	rep := m.changes.report
	if rep.NotRepo {
		return []string{sDim.Render(ansi.Truncate("not a git repository: "+shortPath(rep.Dir), w, "…"))}
	}
	out := []string{changesHead(m.changesLabel(s, role, rep), "D back", w)}
	out = append(out, sDim.Render(ansi.Truncate(changesSummary(rep), w, "…")))
	if len(rep.Files) == 0 && rep.Diff == "" {
		return fit(out, w, h)
	}
	max := 0
	for _, f := range rep.Files {
		max = maxInt(max, f.Added+f.Removed)
	}
	for _, f := range rep.Files {
		out = append(out, changeFileLine(f, max, w))
	}
	if rep.Diff != "" {
		if len(rep.Files) > 0 {
			out = append(out, sRule.Render(strings.Repeat("─", w)))
		}
		room := h - len(out)
		if room > 0 {
			out = append(out, changeDiffLines(rep.Diff, w, room)...)
		}
	}
	return fit(out, w, h)
}

// nodeTeamID is the selected team's ID, on a team or a member row.
func (m Model) nodeTeamID() string {
	n := m.selectedNode()
	if n == nil {
		return ""
	}
	if n.Team != nil {
		return n.Team.ID
	}
	return n.TeamID
}

// changesLabel words what the preview shows, and who else shares a plain
// folder.
func (m Model) changesLabel(s store.Session, role string, rep tracker.ChangeReport) string {
	if rep.Worktree {
		return "changes on " + rep.Branch + " vs " + rep.Base
	}
	label := "changes in this folder"
	if others := m.sharedSessions(s); others > 0 {
		who := title(s)
		if role != "" {
			who = role
		}
		label += fmt.Sprintf(" · shared with %d live sessions, so not only %s's", others, who)
	}
	return label
}

// sharedSessions counts the other live sessions in a session's folder.
func (m Model) sharedSessions(s store.Session) int {
	if s.Cwd == "" {
		return 0
	}
	n := 0
	for _, o := range m.sessions {
		if o.ID != s.ID && o.Live() && o.Kind != store.KindInternal && o.Cwd == s.Cwd {
			n++
		}
	}
	return n
}

// changesHead puts the label on the left and D back on the right.
func changesHead(label, right string, w int) string {
	label = cleanLine(label)
	right = sDim.Render(right)
	room := w - ansi.StringWidth(right) - 1
	if room < 1 {
		return sBold.Render(ansi.Truncate(label, w, "…"))
	}
	label = ansi.Truncate(label, room, "…")
	gap := w - ansi.StringWidth(label) - ansi.StringWidth(right)
	return sBold.Render(label) + strings.Repeat(" ", max(1, gap)) + right
}

// changesSummary counts the files, lines, uncommitted paths and commits.
func changesSummary(rep tracker.ChangeReport) string {
	added, removed := 0, 0
	for _, f := range rep.Files {
		added, removed = added+f.Added, removed+f.Removed
	}
	if len(rep.Files) == 0 && rep.Uncommitted == 0 && rep.Ahead == 0 {
		return "no changes"
	}
	parts := []string{plural(len(rep.Files), "file")}
	if added+removed > 0 {
		parts = append(parts, fmt.Sprintf("+%d −%d", added, removed))
	}
	if rep.Uncommitted > 0 {
		parts = append(parts, fmt.Sprintf("%d uncommitted", rep.Uncommitted))
	}
	if rep.Ahead > 0 {
		parts = append(parts, plural(rep.Ahead, "commit")+" ahead")
	}
	return strings.Join(parts, " · ")
}

// changeFileLine draws one changed path: its status, name, lines and a bar
// against the largest change.
func changeFileLine(f gitx.FileChange, largest, w int) string {
	status := f.Status
	if status == "" {
		status = "M"
	}
	counts := ""
	if f.Added > 0 {
		counts += sOK.Render(fmt.Sprintf("+%d", f.Added))
	}
	if f.Removed > 0 {
		if counts != "" {
			counts += " "
		}
		counts += sError.Render(fmt.Sprintf("−%d", f.Removed))
	}
	pathW := w - 2 - 12 - 1 - 9
	if pathW < 4 {
		pathW = max(4, w-2-9)
	}
	path := pad(ansi.Truncate(cleanLine(f.Path), pathW, "…"), pathW)
	line := sDim.Render(status) + " " + sText.Render(path) + " " + pad(counts, 12) + " " + changeBar(f, largest)
	return ansi.Truncate(line, w, "")
}

// changeBar draws nine cells, filled in proportion to the largest change.
func changeBar(f gitx.FileChange, largest int) string {
	n := 0
	if largest > 0 {
		n = int(math.Round(float64(f.Added+f.Removed) * 9 / float64(largest)))
	}
	if n == 0 && f.Added+f.Removed > 0 {
		n = 1
	}
	n = min(9, max(0, n))
	return sAccent.Render(strings.Repeat("▓", n)) + sRule.Render(strings.Repeat("░", 9-n))
}

// changeDiffLines colours a unified diff: additions green, removals red, the
// hunk headers and file headers dim.
func changeDiffLines(diff string, w, limit int) []string {
	var out []string
	for _, line := range strings.Split(diff, "\n") {
		if len(out) >= limit {
			break
		}
		line = ansi.Truncate(line, w, "…")
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"),
			strings.HasPrefix(line, "@@"), strings.HasPrefix(line, "diff --git"),
			strings.HasPrefix(line, "index "):
			out = append(out, sDim.Render(line))
		case strings.HasPrefix(line, "+"):
			out = append(out, sOK.Render(line))
		case strings.HasPrefix(line, "-"):
			out = append(out, sError.Render(line))
		default:
			out = append(out, sText.Render(line))
		}
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
