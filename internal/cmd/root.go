// Package cmd implements the hive command line.
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// Execute runs hive.
func Execute() {
	root := &cobra.Command{
		Use:   "hive",
		Short: "One tree of all your coding-agent sessions",
		Long: "hive tracks every Claude Code and opencode session, links each agent to the\n" +
			"agents it spawns, and shows them as one tree.",
		SilenceUsage: true,
	}
	root.AddCommand(newHookCmd(), newInstallCmd(), newUninstallCmd(), newLsCmd(), newSyncCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
