// Package cmd implements the hive command line.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/tmux"
)

// Execute runs hive.
func Execute() {
	// HIVE_TMUX_SOCKET points hive at a named tmux server (tmux -L name)
	// instead of the one it runs in or the default.
	tmux.Socket = os.Getenv("HIVE_TMUX_SOCKET")
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
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
		newTailCmd(), newWaitCmd(), newUsageCmd(), newModelsCmd(), newKillCmd(), newDiffCmd(), newTeamCmd(), newRmCmd(), newTrashCmd(), newStatusCmd(), newDoctorCmd())
	groupHelp(root)
	wireCompletion(root)
	return root
}

// groupHelp puts the commands under headings in `hive --help`.
func groupHelp(root *cobra.Command) {
	groups := []struct {
		id, title string
		commands  []string
	}{
		{"start", "Start here:", []string{"install", "doctor"}},
		{"sessions", "Sessions:", []string{"ls", "new", "send", "tail", "wait", "jump", "resume", "kill", "usage", "diff", "models"}},
		{"teams", "Teams:", []string{"team"}},
		{"housekeeping", "Housekeeping:", []string{"sync", "rm", "trash", "uninstall"}},
		{"tmux", "For tmux and hooks:", []string{"status", "popup", "hook"}},
	}
	byName := map[string]*cobra.Command{}
	for _, c := range root.Commands() {
		byName[c.Name()] = c
	}
	for _, g := range groups {
		root.AddGroup(&cobra.Group{ID: g.id, Title: g.title})
		for _, name := range g.commands {
			if c, ok := byName[name]; ok {
				c.GroupID = g.id
			}
		}
	}
}

// oneArg makes cobra's "accepts 1 arg(s)" a sentence that names the command and
// how to call it. noun is what it needs, syntax how to give it, note a hint.
func oneArg(noun, syntax, note string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		switch len(args) {
		case 1:
			return nil
		case 0:
			return fmt.Errorf("%s needs a %s: %s %s%s", cmd.CommandPath(), noun, cmd.CommandPath(), syntax, withHint(note))
		}
		return fmt.Errorf("%s takes one %s: %s %s%s", cmd.CommandPath(), noun, cmd.CommandPath(), syntax, withHint(note))
	}
}

// someArgs is oneArg for a command that takes one or more.
func someArgs(noun, syntax, note string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return nil
		}
		return fmt.Errorf("%s needs a %s: %s %s%s", cmd.CommandPath(), noun, cmd.CommandPath(), syntax, withHint(note))
	}
}

func withHint(note string) string {
	if note == "" {
		return ""
	}
	return " (" + note + ")"
}
