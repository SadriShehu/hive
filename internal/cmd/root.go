// Package cmd implements the hive command line.
package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/tmux"
)

// Execute runs hive.
func Execute() {
	// HIVE_TMUX_SOCKET points hive at a named tmux server (tmux -L name)
	// instead of the one it runs in or the default.
	tmux.Socket = os.Getenv("HIVE_TMUX_SOCKET")
	root := &cobra.Command{
		Use:     "hive",
		Version: resolvedVersion(),
		Short:   "One tree of all your coding-agent sessions",
		Long: "hive tracks every Claude Code, opencode, Copilot CLI and Codex session, links each agent to the\n" +
			"agents it spawns, and shows them as one tree. Run it with no command to open\n" +
			"the tree: jump into any agent, message it, start or reopen one.",
		Args:         cobra.NoArgs,
		RunE:         rootRun,
		SilenceUsage: true,
	}
	root.AddCommand(newHookCmd(), newInstallCmd(), newUninstallCmd(), newLsCmd(), newSyncCmd(),
		newPopupCmd(), newUICmd(), newNewCmd(), newJumpCmd(), newResumeCmd(), newSendCmd(),
		newTailCmd(), newKillCmd(), newRmCmd(), newTrashCmd(), newDoctorCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
