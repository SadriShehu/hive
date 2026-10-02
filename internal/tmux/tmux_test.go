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
	bin := "/Users/me/go/bin/hive"

	msg, err := InstallBindings(conf, PopupBinding("a", bin), NextBinding("A", bin))
	if err != nil || !strings.Contains(msg, "active now") {
		t.Fatalf("install: %q, %v", msg, err)
	}
	data, _ := os.ReadFile(conf)
	popup := popupMarker + "\n" + `bind-key a display-popup -E -w 90% -h 85% -T " hive " "/Users/me/go/bin/hive popup"` + "\n"
	next := nextMarker + "\n" + `bind-key A run-shell -b "/Users/me/go/bin/hive jump --next --client '#{client_name}'"` + "\n"
	if want := "set -g mouse on\n\n" + popup + "\n" + next; string(data) != want {
		t.Fatalf("conf =\n%s\nwant\n%s", data, want)
	}
	keys, _ := run("list-keys", "-T", "prefix")
	if !strings.Contains(keys, `prefix a       display-popup`) || !strings.Contains(keys, "hive popup") ||
		!strings.Contains(keys, `prefix A       run-shell -b "/Users/me/go/bin/hive jump --next --client '#{client_name}'"`) {
		t.Fatalf("live bindings = %q", keys)
	}
	if PopupKey(conf) != "a" || NextKey(conf) != "A" {
		t.Fatalf("keys read back: %q, %q", PopupKey(conf), NextKey(conf))
	}

	// tmux reads the file back to the same bindings.
	run("unbind-key", "a")
	run("unbind-key", "A")
	if _, err := run("source-file", conf); err != nil {
		t.Fatal(err)
	}
	if again, _ := run("list-keys", "-T", "prefix"); again != keys {
		t.Fatalf("after source-file:\n%s\nwant\n%s", again, keys)
	}

	// Rerunning with another binary replaces the lines instead of adding any.
	InstallBindings(conf, PopupBinding("a", "/opt/hive dir/hive"), NextBinding("A", "/opt/hive dir/hive"))
	data, _ = os.ReadFile(conf)
	if strings.Count(string(data), "bind-key") != 2 || !strings.Contains(string(data), `"'/opt/hive dir/hive' popup"`) ||
		!strings.Contains(string(data), `"'/opt/hive dir/hive' jump --next`) {
		t.Fatalf("after reinstall:\n%s", data)
	}

	if _, err := UninstallBindings(conf); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(conf); string(data) != "set -g mouse on\n" {
		t.Fatalf("after uninstall = %q", data)
	}
	if keys, _ := run("list-keys", "-T", "prefix"); strings.Contains(keys, "hive") {
		t.Fatalf("bindings left after uninstall: %q", keys)
	}
}

func TestInstallAddsTheNextKeyToAnOlderInstall(t *testing.T) {
	isolated(t)
	conf := filepath.Join(t.TempDir(), "tmux.conf")
	old := "set -g mouse on\n\n" + popupMarker + "\n" + PopupBinding("a", "/bin/hive").line() + "\n"
	os.WriteFile(conf, []byte(old), 0o644)

	InstallBindings(conf, PopupBinding("a", "/bin/hive"), NextBinding("A", "/bin/hive"))
	data, _ := os.ReadFile(conf)
	if !strings.HasPrefix(string(data), old) || NextKey(conf) != "A" || strings.Count(string(data), "bind-key") != 2 {
		t.Fatalf("conf =\n%s", data)
	}
}

func TestNotifyEscapesFormats(t *testing.T) {
	isolated(t)
	if got := literal("fix #1 #{pane_id} #(touch x)"); got != "fix ##1 ##{pane_id} ##(touch x)" {
		t.Fatalf("literal = %q", got)
	}
	if out, _ := run("display-message", "-p", literal("#(echo ran) #{session_name}")); out != "#(echo ran) #{session_name}" {
		t.Fatalf("tmux showed %q: a title could run commands", out)
	}
	// Nobody is attached to the test server: nobody to tell, and no error.
	panes, _ := Panes()
	if err := Notify("hive: claude ‹x› needs you", panes[0].ID); err != nil {
		t.Fatal(err)
	}
}

func TestFailedProgramLeavesAMessageEnterCloses(t *testing.T) {
	isolated(t)
	failing, err := NewWindow(Window{Session: "base", Name: "claude/app", Detached: true,
		Argv: []string{"/bin/sh", "-c", "echo 'No conversation found' >&2; exit 1"}})
	if err != nil {
		t.Fatal(err)
	}
	clean, _ := NewWindow(Window{Session: "base", Name: "clean", Detached: true, Argv: []string{"/bin/sh", "-c", "exit 0"}})
	alive, _ := NewWindow(Window{Session: "base", Name: "alive", Detached: true, Argv: []string{"sleep", "30"}})

	waitFor(t, "the failure message", func() bool {
		screen, _ := Capture(failing.ID)
		return strings.Contains(screen, "hive: claude/app stopped with an error.") && strings.Contains(screen, "Press Enter")
	})
	if PaneExists(clean.ID) {
		t.Error("a program that exited cleanly left its window open")
	}
	if out, _ := run("display-message", "-p", "-t", alive.ID, "#{pane_pid}"); out != fmt.Sprint(alive.PID) || alive.PID == 0 {
		t.Errorf("pane pid = %s, NewWindow said %d: it must be the program's own", out, alive.PID)
	}

	run("send-keys", "-t", failing.ID, "Enter")
	waitFor(t, "Enter to close the window", func() bool { return !PaneExists(failing.ID) })
}
