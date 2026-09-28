package tmux

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// isolated runs the test against a private tmux server, so nothing touches
// the user's sessions.
func isolated(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("tmux not installed")
	}
	Socket = fmt.Sprintf("hive-test-%d", os.Getpid())
	t.Setenv("TMUX", "") // never inherit the user's server
	if _, err := run("new-session", "-d", "-s", "base", "-x", "120", "-y", "30"); err != nil {
		t.Fatal(err)
	}
	socketPath, _ := run("display-message", "-p", "#{socket_path}")
	t.Cleanup(func() {
		command("kill-server").Run()
		Socket = ""
		if strings.Contains(socketPath, "hive-test-") {
			os.Remove(socketPath)
		}
	})
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for range 100 {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestWindowsPasteAndCapture(t *testing.T) {
	isolated(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "typed.txt")

	// A program that records what is typed into it, plus its environment.
	pane, err := NewWindow(Window{
		Session: "base", Name: "rec", Dir: dir, Detached: true,
		Env:  map[string]string{"HIVE_PARENT": "claude:p1"},
		Argv: []string{"/bin/sh", "-c", `echo "parent=$HIVE_PARENT cwd=$(pwd -P)"; cat > ` + out},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pane.ID, "%") || pane.PID == 0 || pane.Session != "base" {
		t.Fatalf("pane = %+v", pane)
	}
	if !PaneExists(pane.ID) {
		t.Fatal("new pane not listed")
	}

	realDir, _ := filepath.EvalSymlinks(dir)
	waitFor(t, "the program's greeting", func() bool {
		screen, _ := Capture(pane.ID)
		return strings.Contains(screen, "parent=claude:p1 cwd="+realDir)
	})

	if err := Paste(pane.ID, "hello from hive"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the pasted line", func() bool {
		data, _ := os.ReadFile(out)
		return strings.Contains(string(data), "hello from hive\n")
	})

	windows, err := Windows("base")
	if err != nil || len(windows) != 2 || windows[1] != "rec" {
		t.Fatalf("windows = %v, %v", windows, err)
	}
}

func TestFocusRefusesWithoutAClient(t *testing.T) {
	isolated(t)
	panes, _ := Panes()
	if err := Focus(panes[0].ID); err == nil {
		t.Fatal("Focus switched a client on a server nobody is looking at")
	}
}

func TestBindingRoundTrip(t *testing.T) {
	isolated(t)
	conf := filepath.Join(t.TempDir(), "tmux.conf")
	os.WriteFile(conf, []byte("set -g mouse on\n"), 0o644)

	msg, err := InstallBinding(conf, "a", "/Users/me/go/bin/hive")
	if err != nil || !strings.Contains(msg, "active now") {
		t.Fatalf("install: %q, %v", msg, err)
	}
	data, _ := os.ReadFile(conf)
	want := "set -g mouse on\n\n" + bindingMarker + "\n" +
		`bind-key a display-popup -E -w 90% -h 85% -T " hive " "/Users/me/go/bin/hive popup"` + "\n"
	if string(data) != want {
		t.Fatalf("conf =\n%s\nwant\n%s", data, want)
	}
	keys, _ := run("list-keys", "-T", "prefix")
	if !strings.Contains(keys, `prefix a       display-popup`) || !strings.Contains(keys, "hive popup") {
		t.Fatalf("live binding = %q", keys)
	}

	// Rerunning with another binary replaces the line instead of adding one.
	InstallBinding(conf, "a", "/opt/hive dir/hive")
	data, _ = os.ReadFile(conf)
	if strings.Count(string(data), "bind-key") != 1 || !strings.Contains(string(data), `"'/opt/hive dir/hive' popup"`) {
		t.Fatalf("after reinstall:\n%s", data)
	}

	if _, err := UninstallBinding(conf); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(conf); string(data) != "set -g mouse on\n" {
		t.Fatalf("after uninstall = %q", data)
	}
}
