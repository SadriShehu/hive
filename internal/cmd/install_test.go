package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/tmux"
)

func TestStableBinPathPrefersThePathEntryThatLinksToTheSameFile(t *testing.T) {
	resolvedExe := executableFile(t, "hive-real")
	pathDir := t.TempDir()
	link := filepath.Join(pathDir, "hive")
	if err := os.Symlink(resolvedExe, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	if got := stableBinPath(resolvedExe); got != link {
		t.Fatalf("got %q, want %q", got, link)
	}
}

func TestStableBinPathKeepsTheResolvedFileWhenPathHasAnotherHive(t *testing.T) {
	resolvedExe := executableFile(t, "hive-real")
	other := executableFile(t, "hive")
	t.Setenv("PATH", filepath.Dir(other))
	if got := stableBinPath(resolvedExe); got != resolvedExe {
		t.Fatalf("got %q, want %q", got, resolvedExe)
	}
}

func TestStableBinPathKeepsTheResolvedFileWithoutHiveOnPath(t *testing.T) {
	resolvedExe := executableFile(t, "hive-real")
	t.Setenv("PATH", t.TempDir())
	if got := stableBinPath(resolvedExe); got != resolvedExe {
		t.Fatalf("got %q, want %q", got, resolvedExe)
	}
}

func executableFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestStatusLineConfSurvivesReloading(t *testing.T) {
	if !tmux.Available() {
		t.Skip("tmux not installed")
	}
	tmux.Socket = fmt.Sprintf("hive-cmd-test-%d", os.Getpid())
	t.Setenv("TMUX", "") // never the user's server
	t.Cleanup(func() { tmux.Run("kill-server"); tmux.Socket = "" })
	if _, err := tmux.Run("-f", "/dev/null", "new-session", "-d", "-s", "base"); err != nil {
		t.Fatal(err)
	}
	right := `#{?window_bigger,[#{window_offset_x}#,#{window_offset_y}] ,}"#{=21:pane_title}" %H:%M %d-%b-%y`
	tmux.Run("set", "-g", "status-right", right)
	tmux.Run("set", "-g", "status-right-length", "40")

	lines := statusLineConf("/opt/hive dir/hive")
	want := []string{"set -g status-right-length 60",
		"set -g status-right '" + right + ` #("/opt/hive dir/hive" status --tmux)'`}
	if !slices.Equal(lines, want) {
		t.Fatalf("lines =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}

	conf := filepath.Join(t.TempDir(), "tmux.conf")
	os.WriteFile(conf, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	for range 2 { // a reload must not add a second copy
		if _, err := tmux.Run("source-file", conf); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := tmux.Run("show-options", "-gv", "status-right")
	if got != right+` #("/opt/hive dir/hive" status --tmux)` {
		t.Fatalf("status-right after two loads = %q", got)
	}
	if !inStatusLine() {
		t.Fatal("inStatusLine doesn't see the count it just added")
	}
	if lines := statusLineConf("/bin/hive"); len(lines) != 1 || !strings.Contains(lines[0], "/bin/hive status --tmux") {
		t.Fatalf("with enough room, lines = %q, want only status-right", lines)
	}
}
