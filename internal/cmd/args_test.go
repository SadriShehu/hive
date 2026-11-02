package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func findCmd(t *testing.T, root *cobra.Command, path ...string) *cobra.Command {
	t.Helper()
	c, _, err := root.Find(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewRefusesAnUnknownToolBeforeTheDryRun(t *testing.T) {
	t.Setenv("HIVE_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	root := newRootCmd()
	root.SetArgs([]string{"new", "nosuchtool", "-p", "x", "--dry-run"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()
	if err == nil {
		t.Fatal("an unknown tool was accepted")
	}
	for _, want := range []string{`no tool "nosuchtool"`, "claude, opencode, copilot, codex", "[[agent]]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want %q", err, want)
		}
	}
}

func TestRequiredArgsSayHowToCallTheCommand(t *testing.T) {
	root := newRootCmd()
	for _, c := range []struct {
		path []string
		args []string
		want string
	}{
		{[]string{"wait"}, nil, "hive wait needs a session: hive wait <id>… (hive ls lists them)"},
		{[]string{"kill"}, nil, "hive kill needs a session: hive kill <id>"},
		{[]string{"kill"}, []string{"a", "b"}, "hive kill takes one session: hive kill <id>"},
		{[]string{"send"}, nil, "hive send needs a session: hive send <id> [text...] (hive ls lists them)"},
		{[]string{"tail"}, nil, "hive tail needs a session: hive tail <id> (hive ls lists them)"},
		{[]string{"resume"}, nil, "hive resume needs a session: hive resume <id> (hive ls lists them)"},
		{[]string{"usage"}, nil, "hive usage needs a session: hive usage <id> (hive ls lists them)"},
		{[]string{"jump"}, nil, "hive jump needs a session: hive jump <id> (or hive jump --next)"},
		{[]string{"new"}, nil, "hive new needs a tool: hive new <tool|auto> (hive models lists them)"},
		{[]string{"rm"}, nil, "hive rm needs a session: hive rm <id> (hive ls lists them)"},
		{[]string{"trash", "restore"}, nil, "hive trash restore needs a session: hive trash restore <id> (hive trash lists them)"},
		{[]string{"trash", "purge"}, nil, "hive trash purge needs a session: hive trash purge <id> (or --all; hive trash lists them)"},
	} {
		cmd := findCmd(t, root, c.path...)
		err := cmd.Args(cmd, c.args)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s %q = %v, want %q", strings.Join(c.path, " "), c.args, err, c.want)
		}
	}
}

func TestRootWithoutATerminalSaysSo(t *testing.T) {
	old := os.Stdin
	t.Cleanup(func() { os.Stdin = old })
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	os.Stdin = devnull
	if err := rootRun(nil, nil); err == nil || err.Error() != "the tree needs a terminal; hive ls prints it" {
		t.Errorf("rootRun = %v, want the no-terminal sentence", err)
	}
}

func TestHelpGroupsCommands(t *testing.T) {
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	if err := root.Help(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Start here:", "Sessions:", "Teams:", "Housekeeping:", "For tmux and hooks:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help = %q, want the group %q", out.String(), want)
		}
	}
}
