package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tree"
)

// workLine draws the new-team form's work row, which ←/→ flips.
func (f teamForm) workLine() string {
	pick := 0
	if f.work {
		pick = 1
	}
	line := choices([]string{"shared folder", "a worktree per member"}, pick, func(string) lipgloss.Style { return sAccent })
	if f.work {
		line += "  " + sDim.Render("the lead merges")
	}
	return line
}

// foldDone folds a team the first time the tree sees it done, so that it
// stays out of the way with its history; the user can unfold it again.
func (m *Model) foldDone() {
	for _, t := range m.snap.Teams {
		id := tree.TeamPrefix + t.ID
		if t.Status != store.TeamDone || m.foldedDone[id] {
			continue
		}
		if m.foldedDone == nil {
			m.foldedDone = map[string]bool{}
		}
		m.foldedDone[id] = true
		m.collapsed[id] = true
	}
}

func isDone(t *store.Team) bool { return t != nil && t.Status == store.TeamDone }
