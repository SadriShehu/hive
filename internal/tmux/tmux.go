// Package tmux talks to the tmux server that hosts every agent's TUI.
package tmux

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Socket, when set, selects a separate tmux server (tmux -L), for tests.
var Socket string

// Pane is one tmux pane.
type Pane struct {
	ID      string // e.g. "%12"
	PID     int    // the pane's first process, usually a shell
	Session string
	Window  string // window index
	Name    string // window name
}

// Available reports whether tmux is installed.
func Available() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// Inside reports whether this process runs in a tmux pane.
func Inside() bool { return os.Getenv("TMUX") != "" }

func command(args ...string) *exec.Cmd {
	if Socket != "" {
		args = append([]string{"-L", Socket}, args...)
	}
	return exec.Command("tmux", args...)
}

// run runs a tmux command and returns its trimmed output.
func run(args ...string) (string, error) {
	var stderr strings.Builder
	cmd := command(args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("tmux %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("tmux %s: %w", args[0], err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// Panes lists every pane on the server, or none when no server is running.
func Panes() ([]Pane, error) {
	if !Available() {
		return nil, nil
	}
	out, err := command("list-panes", "-a", "-F",
		"#{pane_id}\t#{pane_pid}\t#{session_name}\t#{window_index}\t#{window_name}").Output()
	if err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			return nil, nil // no server running
		}
		return nil, err
	}
	var panes []Pane
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 5 {
			continue
		}
		pid, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		panes = append(panes, Pane{ID: f[0], PID: pid, Session: f[2], Window: f[3], Name: f[4]})
	}
	return panes, nil
}

// PaneExists reports whether pane is on the server.
func PaneExists(pane string) bool {
	panes, _ := Panes()
	for _, p := range panes {
		if p.ID == pane {
			return true
		}
	}
	return false
}

// HasSession reports whether a session with this name exists.
func HasSession(name string) bool {
	return command("has-session", "-t", "="+name).Run() == nil
}

// CurrentSession is the session of the client looking at this process.
func CurrentSession() (string, error) {
	return run("display-message", "-p", "#{session_name}")
}

// HomeSession is where hive opens agent windows: the current session when
// inside tmux, otherwise a detached session called "hive", created on demand.
func HomeSession() (string, error) {
	if Inside() {
		return CurrentSession()
	}
	if !HasSession("hive") {
		home, _ := os.UserHomeDir()
		if _, err := run("new-session", "-d", "-s", "hive", "-c", home); err != nil && !HasSession("hive") {
			return "", err
		}
	}
	return "hive", nil
}

// Window describes a new window running one program.
type Window struct {
	Session  string // "" means HomeSession
	Name     string
	Dir      string
	Env      map[string]string
	Argv     []string // run directly, without a shell
	Detached bool     // don't make it the session's current window
}

// NewWindow opens a window running w.Argv and returns its pane.
//
// A program that exits cleanly closes its window. One that fails leaves a
// short message behind that Enter dismisses, instead of tmux's dead pane,
// which takes no keys at all. The window starts on a placeholder so that
// is in place even for a program that fails at once.
func NewWindow(w Window) (Pane, error) {
	if len(w.Argv) == 0 {
		return Pane{}, errors.New("nothing to run")
	}
	session := w.Session
	if session == "" {
		var err error
		if session, err = HomeSession(); err != nil {
			return Pane{}, err
		}
	}
	var place []string // where the program runs, for both the window and the respawn
	if w.Dir != "" {
		place = append(place, "-c", w.Dir)
	}
	keys := make([]string, 0, len(w.Env))
	for k := range w.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		place = append(place, "-e", k+"="+w.Env[k])
	}

	args := []string{"new-window", "-d", "-P", "-F", "#{pane_id}\t#{window_index}", "-t", session + ":"}
	if w.Name != "" {
		args = append(args, "-n", w.Name)
	}
	args = append(append(args, place...), "--", "sleep", "86400")
	out, err := run(args...)
	if err != nil {
		return Pane{}, err
	}
	id, window, _ := strings.Cut(out, "\t")
	pane := Pane{ID: id, Session: session, Window: window, Name: w.Name}

	label := safeLabel(w.Name, w.Argv[0])
	onFailure := fmt.Sprintf(`set-option -p -t %[1]s remain-on-exit off ; set-hook -pu -t %[1]s pane-died ; `+
		`respawn-pane -k -t %[1]s "echo; echo '  hive: %[2]s stopped with an error.'; echo '  Press Enter to close this window.'; read _"`,
		id, label)
	steps := [][]string{
		{"set-option", "-p", "-t", id, "remain-on-exit", "failed"},
		{"set-hook", "-p", "-t", id, "pane-died", onFailure},
		append(append(append([]string{"respawn-pane", "-k", "-t", id}, place...), "--"), w.Argv...),
	}
	for _, step := range steps {
		if _, err := run(step...); err != nil {
			run("kill-pane", "-t", id)
			return Pane{}, err
		}
	}
	pid, err := run("display-message", "-p", "-t", id, "#{pane_pid}")
	if err != nil {
		return Pane{}, err
	}
	pane.PID, _ = strconv.Atoi(pid)
	if !w.Detached {
		run("select-window", "-t", id)
	}
	return pane, nil
}

// safeLabel names the program in the failure message, keeping only
// characters that are safe inside the quoting there.
func safeLabel(name, program string) string {
	if name == "" {
		name = program
	}
	return strings.Map(func(r rune) rune {
		if r == '\'' || r == '"' || r == '\\' || r == '$' || r == '`' || r == ';' || r == '#' {
			return -1
		}
		return r
	}, name)
}

// Focus shows pane on the client looking at this process, switching session,
// window and pane. It refuses when no client is looking, rather than take
// over some other client's screen.
func Focus(pane string) error {
	client, err := run("display-message", "-p", "#{client_name}")
	if err != nil {
		return err
	}
	if client == "" {
		return errors.New("no tmux client is showing hive")
	}
	_, err = run("switch-client", "-c", client, "-t", pane)
	return err
}

// Paste types text into pane as a bracketed paste, then presses Enter.
func Paste(pane, text string) error {
	buffer := fmt.Sprintf("hive-%d", os.Getpid())
	load := command("load-buffer", "-b", buffer, "-")
	load.Stdin = strings.NewReader(text)
	if out, err := load.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux load-buffer: %v: %s", err, out)
	}
	if _, err := run("paste-buffer", "-p", "-d", "-b", buffer, "-t", pane); err != nil {
		return err
	}
	// Let the program take the paste in before Enter arrives.
	time.Sleep(150 * time.Millisecond)
	_, err := run("send-keys", "-t", pane, "Enter")
	return err
}

// Capture returns what pane shows, with colors.
func Capture(pane string) (string, error) {
	return run("capture-pane", "-p", "-e", "-t", pane)
}

// Attach replaces this process with a tmux client attached to session.
func Attach(session string) error {
	return execTmux("attach-session", "-t", "="+session)
}

// Windows lists the names of a session's windows.
func Windows(session string) ([]string, error) {
	out, err := run("list-windows", "-t", "="+session, "-F", "#{window_name}")
	if err != nil {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// Run runs any tmux command.
func Run(args ...string) (string, error) { return run(args...) }
