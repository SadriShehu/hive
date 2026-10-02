package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
)

func newSyncCmd() *cobra.Command {
	var full bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Import past sessions and link past spawns",
		Long: "Import sessions from each tool's own storage (Claude transcripts, opencode's\n" +
			"database, Copilot CLI's session store, Codex's rollouts) and link spawns found in their\n" +
			"shell commands, in any direction.\n" +
			"Only what changed since the last sync is read; `hive ls` syncs on its own.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(paths.DBPath())
			if err != nil {
				return err
			}
			defer st.Close()
			if full {
				if err := errors.Join(st.ResetImports(), st.ResetUsage()); err != nil {
					return err
				}
			}
			tr := newTracker(st)
			res, err := tr.Sync(cmd.Context())
			drained, _, usageErr := tr.RefreshUsage(cmd.Context(), 0)
			var parts []string
			for _, a := range tr.Adapters {
				if n, ok := res.Imported[a.Spec().Name]; ok {
					parts = append(parts, fmt.Sprintf("%s %d", a.Spec().Name, n))
				}
			}
			fmt.Printf("sessions read: %s · past spawns linked: %d · usage read: %d\n",
				strings.Join(parts, ", "), res.Linked, res.UsageRead+drained)
			return errors.Join(err, usageErr)
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "re-read everything, not just what changed")
	return cmd
}

// syncQuietly brings history up to date before showing it; problems are
// reported but don't stop the listing.
func syncQuietly(ctx context.Context, tr *tracker.Tracker) {
	if _, err := tr.Sync(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "sync:", err)
	}
}
