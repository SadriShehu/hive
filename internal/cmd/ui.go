package cmd

import (
	"errors"
	"slices"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tmux"
	"github.com/sadrishehu/hive/internal/tui"
)

// rootRun opens the tree: in this pane when inside tmux, otherwise in the
// "hive" tmux session.
func rootRun(cmd *cobra.Command, args []string) error {
	if !tmux.Available() {
		return errors.New("hive needs tmux to host agents: brew install tmux")
	}
	if !tmux.Inside() {
		return openInTmux()
	}
	return runUI(false)
}

func newPopupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "popup",
		Short: "The tree for a tmux popup: closes once you jump somewhere",
		Long:  "The tree for a tmux popup, as bound by `hive install tmux`: it closes once you\njump to, reopen or start an agent.",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return runUI(true) },
	}
}

func newUICmd() *cobra.Command {
	return &cobra.Command{
		Use:    "ui",
		Short:  "The tree, in this terminal",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   func(cmd *cobra.Command, args []string) error { return runUI(false) },
	}
}

func runUI(popup bool) error {
	st, err := store.Open(paths.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	return tui.Run(tui.NewLiveOps(st, adapters.All()), popup)
}

// openInTmux attaches to the "hive" session, with the tree in a window of
// its own, creating either as needed.
func openInTmux() error {
	self, err := resolveBin("")
	if err != nil {
		return err
	}
	switch {
	case !tmux.HasSession("hive"):
		if _, err := tmux.Run("new-session", "-d", "-s", "hive", "-n", "hive", "-c", paths.Home(), self, "ui"); err != nil {
			return err
		}
	default:
		if windows, _ := tmux.Windows("hive"); !slices.Contains(windows, "hive") {
			if _, err := tmux.Run("new-window", "-d", "-t", "=hive:", "-n", "hive", self, "ui"); err != nil {
				return err
			}
		}
	}
	tmux.Run("select-window", "-t", "=hive:hive")
	return tmux.Attach("hive")
}
