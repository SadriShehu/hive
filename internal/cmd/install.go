package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/tmux"
)

func newInstallCmd() *cobra.Command {
	var bin, key string
	cmd := &cobra.Command{
		Use:   "install [claude|opencode|copilot|codex|tmux ...]",
		Short: "Connect agents and tmux to hive; safe to rerun",
		Long: "Connect agents to hive, and bind the tree to a tmux key. With no arguments:\n" +
			"every agent found on PATH, and tmux when it is installed.\n\n" +
			"Claude Code gets hooks in its settings.json (backed up to settings.json.bak-hive);\n" +
			"opencode gets a plugin file; Copilot CLI and Codex get a backed-up user-level hook file;\n" +
			"tmux gets prefix+a (see --key) opening the tree in a popup. `hive uninstall` removes exactly these.",
		RunE: func(cmd *cobra.Command, args []string) error {
			hiveBin, err := resolveBin(bin)
			if err != nil {
				return err
			}
			agents, withTmux, err := pickTargets(args, true)
			if err != nil {
				return err
			}
			var errs []error
			for _, a := range agents {
				msg, err := a.Install(hiveBin)
				report(a.Spec().Name, msg, err)
				errs = append(errs, err)
			}
			if withTmux {
				msg, err := tmux.InstallBinding(tmux.ConfPath(), key, hiveBin)
				report("tmux", msg, err)
				errs = append(errs, err)
			}
			return errors.Join(errs...)
		},
	}
	cmd.Flags().StringVar(&bin, "bin", "", "hive binary the integrations should call (default: this one)")
	cmd.Flags().StringVar(&key, "key", "a", "tmux key that, after the prefix, opens the tree")
	return cmd
}

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall [claude|opencode|copilot|codex|tmux ...]",
		Short: "Remove exactly what install added",
		RunE: func(cmd *cobra.Command, args []string) error {
			agents, withTmux, err := pickTargets(args, false)
			if err != nil {
				return err
			}
			var errs []error
			for _, a := range agents {
				msg, err := a.Uninstall()
				report(a.Spec().Name, msg, err)
				errs = append(errs, err)
			}
			if withTmux {
				msg, err := tmux.UninstallBinding(tmux.ConfPath())
				report("tmux", msg, err)
				errs = append(errs, err)
			}
			return errors.Join(errs...)
		},
	}
}

func report(name, msg string, err error) {
	if err != nil {
		msg = "error: " + err.Error()
	}
	fmt.Printf("%-9s %s\n", name, msg)
}

// pickTargets resolves install and uninstall arguments. With none: every
// agent (for install, those on PATH) and tmux when it is installed.
func pickTargets(args []string, install bool) (agents []agent.Adapter, withTmux bool, err error) {
	if len(args) == 0 {
		for _, a := range adapters.All() {
			if _, err := exec.LookPath(a.Spec().New[0]); err == nil || !install {
				agents = append(agents, a)
			}
		}
		return agents, tmux.Available(), nil
	}
	for _, n := range args {
		if n == "tmux" {
			withTmux = true
			continue
		}
		a, ok := adapters.Get(n)
		if !ok {
			return nil, false, fmt.Errorf("unknown target %q (agents: claude, opencode, copilot, codex; or tmux)", n)
		}
		agents = append(agents, a)
	}
	return agents, withTmux, nil
}

// resolveBin returns the absolute path integrations should call.
func resolveBin(flag string) (string, error) {
	if flag != "" {
		return filepath.Abs(flag)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	if strings.Contains(exe, "go-build") {
		return "", errors.New("this is a `go run` binary that will disappear; `go install` hive first or pass --bin")
	}
	return exe, nil
}
