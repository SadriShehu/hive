package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
)

func newTeamDoneCmd() *cobra.Command {
	var clean bool
	cmd := &cobra.Command{
		Use:   "done <team>",
		Short: "Mark a team's goal met; it stays in the tree with its history and takes no more mail",
		Long: "Mark the goal met. The team stays in the tree, folded, with its channel; messages to it are\n" +
			"refused, and what was queued is dropped. Nobody is stopped: `hive team stop` does that.\n\n" +
			"--clean removes the worktrees whose branches are merged into the branch the team's folder\n" +
			"had when the team started, with their branches. A worktree stays when its branch has\n" +
			"commits that branch lacks, when it holds changes not committed, or when its member still\n" +
			"runs; `hive team board` lists what stays. Run it again once those are dealt with.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			tm, err := e.team(args[0])
			if err != nil {
				return err
			}
			d, err := e.tr.DoneTeam(tm, e.sender(tm, ""), clean)
			printDone(tm, d, clean, err)
			return err
		},
	}
	cmd.Flags().BoolVar(&clean, "clean", false, "remove the worktrees whose branches are merged, with their branches")
	return cmd
}

// printDone says what `hive team done` did. err is what went wrong, which
// cobra prints after it.
func printDone(tm store.Team, d tracker.Done, clean bool, err error) {
	switch {
	case d.Already:
		fmt.Printf("%s was done already\n", tm.Name)
	case d.Dropped == 1:
		fmt.Printf("%s is done; the 1 message that waited was dropped\n", tm.Name)
	case d.Dropped > 1:
		fmt.Printf("%s is done; the %d messages that waited were dropped\n", tm.Name, d.Dropped)
	default:
		fmt.Printf("%s is done\n", tm.Name)
	}
	if len(d.Running) > 0 {
		fmt.Printf("still running: %s; `hive team stop %s` stops them\n", strings.Join(d.Running, ", "), tm.Name)
	}
	switch {
	case !clean:
	case tm.Workspace != store.WorkspaceWorktrees:
		fmt.Println("its members share one folder: there are no worktrees to clean")
	case len(d.Worktrees) == 0 && err == nil:
		fmt.Println("no worktrees are left")
	case len(d.Worktrees) > 0:
		fmt.Printf("worktrees, against %s\n", d.Base)
		for _, w := range d.Worktrees {
			what, why := "kept", w.State()
			if w.Removed {
				what, why = "removed", w.Note // a note on a removed one: its branch stays
			}
			fmt.Println(strings.TrimRight(fmt.Sprintf("  %-8s %-10s %-24s %s", what, w.Role, w.Branch, why), " "))
		}
	}
}
