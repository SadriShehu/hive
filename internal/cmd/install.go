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
)

func newInstallCmd() *cobra.Command {
	var bin string
	cmd := &cobra.Command{
		Use:   "install [claude|opencode ...]",
		Short: "Connect agents to hive (hooks, plugin); safe to rerun",
		Long: "Connect agents to hive. With no arguments, every agent found on PATH.\n" +
			"Claude Code gets hooks in its settings.json (backed up to settings.json.bak-hive);\n" +
			"opencode gets a plugin file. Running sessions pick the change up after a restart.",
		RunE: func(cmd *cobra.Command, args []string) error {
			hiveBin, err := resolveBin(bin)
			if err != nil {
				return err
			}
			targets, err := pickAdapters(args)
			if err != nil {
				return err
			}
			var errs []error
			for _, a := range targets {
				msg, err := a.Install(hiveBin)
				report(a, msg, err)
				errs = append(errs, err)
			}
			return errors.Join(errs...)
		},
	}
	cmd.Flags().StringVar(&bin, "bin", "", "hive binary the integrations should call (default: this one)")
	return cmd
}

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall [claude|opencode ...]",
		Short: "Remove exactly what install added",
		RunE: func(cmd *cobra.Command, args []string) error {
			targets := adapters.All()
			if len(args) > 0 {
				var err error
				if targets, err = named(args); err != nil {
					return err
				}
			}
			var errs []error
			for _, a := range targets {
				msg, err := a.Uninstall()
				report(a, msg, err)
				errs = append(errs, err)
			}
			return errors.Join(errs...)
		},
	}
}

func report(a agent.Adapter, msg string, err error) {
	if err != nil {
		msg = "error: " + err.Error()
	}
	fmt.Printf("%-9s %s\n", a.Spec().Name, msg)
}

// pickAdapters returns the named adapters, or every one whose tool is on PATH.
func pickAdapters(args []string) ([]agent.Adapter, error) {
	if len(args) > 0 {
		return named(args)
	}
	var out []agent.Adapter
	for _, a := range adapters.All() {
		if _, err := exec.LookPath(a.Spec().New[0]); err == nil {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no supported agent found on PATH")
	}
	return out, nil
}

func named(args []string) ([]agent.Adapter, error) {
	var out []agent.Adapter
	for _, n := range args {
		a, ok := adapters.Get(n)
		if !ok {
			return nil, fmt.Errorf("unknown agent %q", n)
		}
		out = append(out, a)
	}
	return out, nil
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
