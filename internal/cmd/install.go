package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/tmux"
)

func newInstallCmd() *cobra.Command {
	var bin, key, nextKey string
	cmd := &cobra.Command{
		Use:   "install [claude|opencode|copilot|codex|tmux ...]",
		Short: "Connect agents and tmux to hive; safe to rerun",
		Long: "Connect agents to hive, and bind the tree to a tmux key. With no arguments:\n" +
			"every agent found on PATH, and tmux when it is installed.\n\n" +
			"Claude Code gets hooks in its settings.json (backed up to settings.json.bak-hive);\n" +
			"opencode gets a plugin file; Copilot CLI and Codex get a backed-up user-level hook file;\n" +
			"tmux gets prefix+a (see --key) opening the tree in a popup, and prefix+A (see --next-key)\n" +
			"jumping to the agent that needs you. `hive uninstall` removes exactly these.",
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
				msg, err := tmux.InstallBindings(tmux.ConfPath(),
					tmux.PopupBinding(key, hiveBin), tmux.NextBinding(nextKey, hiveBin))
				report("tmux", msg, err)
				errs = append(errs, err)
				if err == nil && !inStatusLine() {
					report("", "to count the agents that need you in tmux's status line, add to "+tmux.ConfPath()+":", nil)
					for _, line := range statusLineConf(hiveBin) {
						report("", "  "+line, nil)
					}
				}
			}
			return errors.Join(errs...)
		},
	}
	cmd.Flags().StringVar(&bin, "bin", "", "hive binary the integrations should call (default: this one)")
	cmd.Flags().StringVar(&key, "key", "a", "tmux key that, after the prefix, opens the tree")
	cmd.Flags().StringVar(&nextKey, "next-key", "A", "tmux key that, after the prefix, jumps to the agent that needs you")
	return cmd
}

// statusLineConf returns the tmux.conf lines that add `hive status` to the
// right side of tmux's status line as it is now. They set the whole value:
// `set -ag` would add another copy each time tmux.conf is loaded.
func statusLineConf(hiveBin string) []string {
	bin := hiveBin
	if strings.ContainsAny(bin, " \t") {
		bin = `"` + bin + `"` // tmux runs it with sh; the line around it is single-quoted
	}
	count := "#(" + bin + " status --tmux)"
	right, err := tmux.Run("show-options", "-gv", "status-right")
	if err != nil || strings.Contains(right, "'") {
		return []string{"(add ' " + count + "' to the end of your status-right)"}
	}
	var lines []string
	if n, err := tmux.Run("show-options", "-gv", "status-right-length"); err == nil {
		if width, _ := strconv.Atoi(n); width > 0 && width < 60 { // tmux's default, 40, cuts the count off
			lines = append(lines, "set -g status-right-length 60")
		}
	}
	return append(lines, "set -g status-right '"+strings.TrimSpace(right+" "+count)+"'")
}

// inStatusLine reports whether tmux's status line already runs `hive status`.
func inStatusLine() bool {
	for _, side := range []string{"status-left", "status-right"} {
		if v, err := tmux.Run("show-options", "-gv", side); err == nil && strings.Contains(v, " status") && strings.Contains(v, "hive") {
			return true
		}
	}
	return false
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
				msg, err := tmux.UninstallBindings(tmux.ConfPath())
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
	return stableBinPath(exe), nil
}

func stableBinPath(resolvedExe string) string {
	onPath, err := exec.LookPath("hive")
	if err != nil {
		return resolvedExe
	}
	if onPath, err = filepath.Abs(onPath); err != nil {
		return resolvedExe
	}
	if target, err := filepath.EvalSymlinks(onPath); err != nil || target != resolvedExe {
		return resolvedExe
	}
	return onPath
}
