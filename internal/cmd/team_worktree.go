package cmd

import (
	"fmt"
	"path/filepath"

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
	for _, w := range b.Trees {
		fmt.Printf("  %-10s %-24s %-22s %s\n", w.Role, w.Branch, w.State(), homePath(w.Path))
	}
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
