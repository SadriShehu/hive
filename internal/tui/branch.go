package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/gitx"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
)

// branchesEvery is how often a team's branches are read again.
const branchesEvery = 10 * time.Second

// BranchReport is what git says about a team: each member's branch on a
// worktrees team, or the folder its members share.
type BranchReport struct {
	Worktrees   bool
	Base        string
	Trees       []tracker.WorktreeState
	Head        string // the shared folder's branch, or short hash
	Uncommitted int    // paths with changes in the shared folder
}

// branchFetch is a team's last report, when it came, and what failed.
type branchFetch struct {
	report BranchReport
	at     time.Time
	got    bool
	err    string
}

type branchesMsg struct {
	teamID string
	report BranchReport
	err    error
	at     time.Time
}

// branchTeam is the team the screen needs branches for: the open channel's,
// or the selected team's or member's.
func (m Model) branchTeam() (store.Team, bool) {
	if m.mode == modeChannel && m.channelTeam != "" {
		return m.teamByID(m.channelTeam)
	}
	n := m.selectedNode()
	if n == nil {
		return store.Team{}, false
	}
	if n.Team != nil {
		return m.teamByID(n.Team.ID)
	}
	if n.TeamID != "" {
		return m.teamByID(n.TeamID)
	}
	return store.Team{}, false
}

// branchesCmd reads the selected team's branches when its report is older
// than branchesEvery. Until the report arrives the team head draws no branch
// lines, so it doesn't jump twice.
func (m *Model) branchesCmd() tea.Cmd {
	t, ok := m.branchTeam()
	if !ok {
		return nil
	}
	now := m.now()
	if f, ok := m.branches[t.ID]; ok && now.Sub(f.at) < branchesEvery {
		return nil
	}
	if m.branches == nil {
		m.branches = map[string]branchFetch{}
	}
	// Keep the last report on screen while git answers again, so the head
	// doesn't lose its branch lines for a frame every refresh.
	f := m.branches[t.ID]
	f.at = now
	m.branches[t.ID] = f
	ops := m.ops
	return func() tea.Msg {
		report, err := ops.TeamBranches(t.ID)
		return branchesMsg{teamID: t.ID, report: report, err: err, at: now}
	}
}

// memberBranches returns the line drawn under each member of a worktrees
// team, or the one line a shared folder gets, or the error. It returns
// nothing until git has answered.
func (m Model) memberBranches(t store.Team, w int) (under map[string]string, shared, errLine string) {
	f, ok := m.branches[t.ID]
	if !ok || !f.got {
		return nil, "", ""
	}
	if f.err != "" {
		return nil, "", sDim.Render(ansi.Truncate("⎇ ? "+f.err, w, "…"))
	}
	if t.Workspace == store.WorkspaceWorktrees {
		under = make(map[string]string, len(f.report.Trees))
		for _, st := range f.report.Trees {
			under[st.Role] = branchLine(st, w)
		}
		return under, "", ""
	}
	return nil, sharedBranchLine(f.report, w), ""
}

// sharedBranchLine words where a shared folder stands: its branch and how
// many paths in it are uncommitted.
func sharedBranchLine(rep BranchReport, w int) string {
	if rep.Head == "" {
		return ""
	}
	line := "⎇ " + rep.Head
	if rep.Uncommitted > 0 {
		word := fmt.Sprintf("%d files uncommitted", rep.Uncommitted)
		if rep.Uncommitted == 1 {
			word = "1 file uncommitted"
		}
		line += " · " + word
	}
	return sDim.Render(ansi.Truncate(line, w, "…"))
}

// branchLine draws one member's branch. The merge result is the only part
// with colour; at a narrow width the diff stat goes first, then how far the
// branch is behind, then the branch name is cut—never the merge result.
func branchLine(st tracker.WorktreeState, w int) string {
	switch {
	case st.Removed:
		return sDim.Render(ansi.Truncate("⎇ "+st.Branch+"  removed", w, "…"))
	case st.Ahead < 0 && st.Note != "":
		return sDim.Render(ansi.Truncate("⎇ ? "+st.Note, w, "…"))
	}
	branch := "⎇ " + st.Branch
	merge := st.MergeWords()
	style := mergeStyle(st.Merge)
	if ansi.StringWidth(merge) > w {
		merge = ansi.Truncate(merge, w, "…")
	}
	tail := style.Render(merge)
	room := w - ansi.StringWidth(tail) - 2
	if room < 1 {
		return tail
	}
	arrows, ahead := st.Arrows(), ""
	if st.Ahead > 0 {
		ahead = fmt.Sprintf("↑%d", st.Ahead)
	}
	diff := st.DiffWords()
	if st.Merge == gitx.Merged { // merged says it all; only changes left in the worktree remain
		arrows, ahead, diff = "", "", ""
		if st.Uncommitted > 0 {
			diff = fmt.Sprintf("· %d uncommitted", st.Uncommitted)
		}
	}
	for _, c := range []string{joinWords(branch, arrows, diff), joinWords(branch, arrows), joinWords(branch, ahead)} {
		if ansi.StringWidth(c) <= room {
			return sDim.Render(c) + "  " + tail
		}
	}
	for _, c := range []string{ahead, ""} {
		avail := room - ansi.StringWidth(c)
		if c != "" {
			avail -= 2
		}
		if avail < 1 {
			continue
		}
		cut := joinWords(ansi.Truncate(branch, avail, "…"), c)
		if ansi.StringWidth(cut) <= room {
			return sDim.Render(cut) + "  " + tail
		}
	}
	return tail
}

// joinWords joins the parts that aren't empty with two spaces.
func joinWords(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "  ")
}

// mergeStyle colours a dry merge's result.
func mergeStyle(result string) lipgloss.Style {
	switch result {
	case gitx.Clean, gitx.Merged:
		return sOK
	case gitx.Conflicts:
		return sError
	}
	return sDim
}
