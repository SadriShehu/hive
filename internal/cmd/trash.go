package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
	"github.com/sadrishehu/hive/internal/tree"
)

// Deleting goes in two steps: `hive rm` moves sessions to the trash, where
// hive keeps them out of the tree and the tools keep their own copies; from
// there `hive trash restore` brings them back, and `hive trash purge` deletes
// them for good, from the tools too.

func newRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm <id>",
		Aliases: []string{"delete"},
		Short:   "Move a session and everything under it to the trash",
		Long: "Move a session and every session under it to hive's trash: they leave the tree, and\n" +
			"history imports leave them there. Everything in there must have ended first (`hive kill`).\n" +
			"`hive trash` lists the trash, to restore sessions or delete them for good. A session in\n" +
			"the trash comes back on its own if its agent reports in again.",
		Args: cobra.ExactArgs(1),
		RunE: withSession(func(cmd *cobra.Command, tr *tracker.Tracker, s store.Session, _ []string) error {
			family, err := tr.Trash(s)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "moved %s to the trash\n", withBelow(s.ID, len(family)-1))
			return nil
		}),
	}
}

func newTrashCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "trash",
		Short: "List the trash, to restore sessions or delete them for good",
		Long: "List the sessions in hive's trash, which `hive rm` puts there. `hive trash restore <id>`\n" +
			"brings one back with everything under it; `hive trash purge <id>` deletes it for good,\n" +
			"from hive and from its tool's own storage, transcripts and all.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(paths.DBPath())
			if err != nil {
				return err
			}
			defer st.Close()
			trashed, err := st.Trashed()
			if err != nil {
				return err
			}
			roots := tree.Build(trashed)
			if asJSON {
				return printJSON(roots)
			}
			if len(trashed) == 0 {
				fmt.Println("the trash is empty")
				return nil
			}
			printTree(roots, func(int) string {
				return fmt.Sprintf("%d sessions in the trash · `hive trash restore <id>` · `hive trash purge <id>`", len(trashed))
			})
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON: a flat list in tree order, with depth")
	cmd.AddCommand(newRestoreCmd(), newPurgeCmd())
	return cmd
}

func newRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <id>",
		Short: "Bring a session and everything under it back from the trash",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, tr, trashed, err := openTrash()
			if err != nil {
				return err
			}
			defer st.Close()
			s, err := lookupTrashed(trashed, args[0])
			if err != nil {
				return err
			}
			family, err := tr.Restore(s)
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "restored %s\n", withBelow(s.ID, len(family)-1))
			return nil
		},
	}
}

func newPurgeCmd() *cobra.Command {
	var all, yes bool
	cmd := &cobra.Command{
		Use:   "purge <id> | --all",
		Short: "Delete sessions in the trash for good, from their tools too",
		Long: "Delete a session in the trash, and everything under it there, for good: from its tool's\n" +
			"own storage (the transcript and whatever else the tool keeps for it), then from hive.\n" +
			"There is no undo. --all empties the trash. It asks first unless --yes; with no terminal\n" +
			"to ask on, it needs --yes.",
		Args: func(cmd *cobra.Command, args []string) error {
			if all {
				return cobra.NoArgs(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			st, tr, trashed, err := openTrash()
			if err != nil {
				return err
			}
			defer st.Close()
			var targets []store.Session
			what := fmt.Sprintf("all %d sessions in the trash", len(trashed))
			if all {
				for _, root := range tree.Build(trashed) {
					targets = append(targets, root.Session)
				}
			} else {
				s, err := lookupTrashed(trashed, args[0])
				if err != nil {
					return err
				}
				family, err := st.Family(s.ID, true)
				if err != nil {
					return err
				}
				targets, what = []store.Session{s}, withBelow(s.ID, len(family)-1)
			}
			if len(targets) == 0 {
				fmt.Fprintln(os.Stderr, "the trash is empty")
				return nil
			}
			if !yes {
				ok, err := confirm("delete " + what + " for good, from their tools too, transcripts and all?")
				if err != nil || !ok {
					return err
				}
			}
			for _, s := range targets {
				purged, err := tr.Purge(cmd.Context(), s)
				if n := len(purged.Sessions); n > 0 {
					fmt.Fprintf(os.Stderr, "deleted %s for good\n", withBelow(s.ID, n-1))
				}
				if len(purged.Kept) > 0 {
					fmt.Fprintf(os.Stderr, "hive can't delete %s sessions: their own copies stay\n", strings.Join(purged.Kept, ", "))
				}
				if err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "empty the trash")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask first")
	return cmd
}

// openTrash opens the database and returns what is in the trash. The caller
// closes the store.
func openTrash() (*store.Store, *tracker.Tracker, []store.Session, error) {
	st, err := store.Open(paths.DBPath())
	if err != nil {
		return nil, nil, nil, err
	}
	trashed, err := st.Trashed()
	if err != nil {
		st.Close()
		return nil, nil, nil, err
	}
	return st, tracker.New(st, adapters.All(), &tracker.System{}), trashed, nil
}

func lookupTrashed(trashed []store.Session, ref string) (store.Session, error) {
	s, err := tracker.Lookup(trashed, ref)
	if errors.Is(err, tracker.ErrNoSession) {
		return s, fmt.Errorf("nothing in the trash matches %q; `hive trash` lists it", ref)
	}
	return s, err
}

// confirm asks question on the terminal. Without one, there is no one to
// ask, and it says how to go ahead anyway.
func confirm(question string) (bool, error) {
	if !term.IsTerminal(os.Stdin.Fd()) {
		return false, errors.New("this deletes for good: pass --yes to go ahead")
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", question)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a == "y" || a == "yes" {
		return true, nil
	}
	fmt.Fprintln(os.Stderr, "nothing deleted")
	return false, nil
}

// withBelow names a session and how many go with it.
func withBelow(name string, below int) string {
	switch below {
	case 0:
		return name
	case 1:
		return name + " and the 1 session under it"
	}
	return fmt.Sprintf("%s and the %d sessions under it", name, below)
}
