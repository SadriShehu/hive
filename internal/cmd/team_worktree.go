package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/sadrishehu/hive/internal/gitx"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
	"github.com/sadrishehu/hive/internal/tracker"
)

// worktreeBoard is what the board shows of a worktrees team's worktrees.
type worktreeBoard struct {
	Base  string                  `json:"base"` // what the branches are merged into
	Trees []tracker.WorktreeState `json:"trees"`
	Error string                  `json:"error,omitempty"`
}

// teamWorktrees reads the worktrees a team still has, for the board; nil for
// a team whose members share one folder.
func teamWorktrees(tr *tracker.Tracker, tm store.Team) *worktreeBoard {
	if tm.Workspace != store.WorkspaceWorktrees {
		return nil
	}
	trees, base, err := tr.TeamWorktrees(tm)
	b := &worktreeBoard{Base: base, Trees: trees}
	if b.Trees == nil {
		b.Trees = []tracker.WorktreeState{}
	}
	if err != nil {
		b.Error = err.Error()
	}
	return b
}

func (b *worktreeBoard) print() {
	switch {
	case b == nil:
		return
	case b.Error != "":
		fmt.Printf("\nworktrees\n  %s\n", b.Error)
		return
	case len(b.Trees) == 0:
		fmt.Println("\nworktrees\n  (none left)")
		return
	}
	fmt.Printf("\nworktrees, against %s\n", b.Base)
	width := boardWidth()
	for _, w := range b.Trees {
		line := "  " + pad(w.Role, 10) + " " + worktreeText(w)
		if room := width - utf8.RuneCountInString(line) - 2; room >= 12 {
			line += "  " + truncate(homePath(w.Path), room)
		}
		fmt.Println(truncate(line, width))
	}
}

// worktreeText words a worktree's branch the way the TUI draws it: the branch,
// how far it is from the base, what it changes, and how a dry merge goes.
func worktreeText(w tracker.WorktreeState) string {
	switch {
	case w.Removed:
		return "⎇ " + w.Branch + "  removed"
	case w.Ahead < 0 && w.Note != "":
		return "⎇ ? " + w.Note
	}
	var parts []string
	parts = append(parts, "⎇ "+w.Branch)
	if w.Merge != gitx.Merged {
		if arrows := w.Arrows(); arrows != "" {
			parts = append(parts, arrows)
		}
		if diff := w.DiffWords(); diff != "" {
			parts = append(parts, diff)
		}
	} else if w.Uncommitted > 0 {
		parts = append(parts, fmt.Sprintf("· %d uncommitted", w.Uncommitted))
	}
	parts = append(parts, w.MergeWords())
	return strings.Join(parts, "  ")
}

// printWorkspace says where the members of a worktrees team work.
func printWorkspace(tm store.Team) {
	if tm.Workspace != store.WorkspaceWorktrees {
		return
	}
	who := "each member"
	if tm.LeadRole != "" {
		who = "each member but " + tm.LeadRole
	}
	fmt.Printf("%s works in a worktree under %s, on %s\n", who,
		homePath(filepath.Dir(tracker.WorktreeDir(tm.Name, "role"))), team.Branch(tm.Name, "<role>"))
}
