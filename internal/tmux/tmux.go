// Package tmux talks to the tmux server that hosts every agent's TUI.
package tmux

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
)

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

// Panes lists every pane on the server, or none when no server is running.
func Panes() ([]Pane, error) {
	if !Available() {
		return nil, nil
	}
	out, err := exec.Command("tmux", "list-panes", "-a", "-F",
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
