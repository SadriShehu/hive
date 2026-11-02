package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/agent/claude"
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

// tempClaudeHome points Claude Code's files at a temp home, so an install
// there never touches the user's own settings.
func tempClaudeHome(t *testing.T) (home, settings string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "COPILOT_HOME", "CODEX_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(key, "")
	}
	settings = filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, settings
}

func TestInstallDryRunShowsTheClaudeDiffAndWritesNothing(t *testing.T) {
	_, settings := tempClaudeHome(t)
	original := []byte("{\n  \"theme\": \"dark\"\n}\n")
	if err := os.WriteFile(settings, original, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(settings)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := dryRunInstall([]agent.Adapter{claude.New()}, false, "/usr/local/bin/hive", "a", "A", false, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"claude: " + settings,
		"--- a/" + strings.TrimPrefix(settings, "/"),
		"+++ b/" + strings.TrimPrefix(settings, "/"),
		`+            "command": "/usr/local/bin/hive hook claude"`,
		`+  "theme": "dark",`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dry run lacks %q:\n%s", want, got)
		}
	}
	after, err := os.Stat(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("dry run changed the file's mtime")
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, original) {
		t.Errorf("dry run changed settings.json:\n%s", data)
	}
	if _, err := os.Stat(settings + ".bak-hive"); !os.IsNotExist(err) {
		t.Error("dry run wrote a backup")
	}
}

func TestInstallDryRunSaysWhenATargetIsUpToDate(t *testing.T) {
	_, settings := tempClaudeHome(t)
	if _, err := claude.New().Install("/usr/local/bin/hive"); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := dryRunInstall([]agent.Adapter{claude.New()}, false, "/usr/local/bin/hive", "a", "A", false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "claude: up to date") {
		t.Fatalf("dry run = %q, want claude: up to date", out.String())
	}
	if _, err := os.Stat(settings + ".bak-hive"); !os.IsNotExist(err) {
		t.Error("dry run wrote a backup")
	}
}

func TestUninstallDryRunShowsTheRemoval(t *testing.T) {
	_, settings := tempClaudeHome(t)
	if _, err := claude.New().Install("/usr/local/bin/hive"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := dryRunInstall([]agent.Adapter{claude.New()}, false, "", "a", "A", true, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "claude: "+settings) || !strings.Contains(got, "--- a/"+strings.TrimPrefix(settings, "/")) {
		t.Fatalf("uninstall dry run = %q", got)
	}
	if !strings.Contains(got, "-            \"command\": \"/usr/local/bin/hive hook claude\"") {
		t.Fatalf("uninstall dry run does not show the hook going:\n%s", got)
	}
	after, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Error("uninstall dry run changed settings.json")
	}
}
