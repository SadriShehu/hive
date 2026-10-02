package cmd

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tmux"
	"github.com/sadrishehu/hive/internal/tracker"
)

// alert tells the user that s needs them, the ways config.toml's [alerts]
// asks for: a message on every tmux client not already looking at it, and a
// desktop notification. It runs in a hook, which never prints, so what goes
// wrong goes to the log.
func alert(st *store.Store, s store.Session) {
	cfg, _ := adapters.LoadAlerts(paths.ConfigPath())
	text := label(s) + " needs you"
	if cfg.Tmux {
		pane := tracker.PaneFor(s, func(id string) (store.Session, bool) {
			p, ok, _ := st.Get(id)
			return p, ok
		})
		hint := ""
		if key := tmux.NextKey(tmux.ConfPath()); key != "" {
			hint = " · prefix " + key + " jumps there"
		}
		if err := tmux.Notify("hive: "+text+hint, pane); err != nil {
			logf("alert %s: %v", s.ID, err)
		}
	}
	if cfg.Desktop {
		if err := notifyDesktop("hive", text); err != nil {
			logf("alert %s: %v", s.ID, err)
		}
	}
}

// notifyDesktop shows a notification without waiting for it to go up:
// osascript on macOS, notify-send elsewhere.
func notifyDesktop(title, text string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		// Through argv, so nothing in the text needs escaping.
		cmd = exec.Command("osascript", "-e", "on run argv", "-e",
			"display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", title, text)
	} else {
		path, err := exec.LookPath("notify-send")
		if err != nil {
			return errors.New("desktop alerts need notify-send")
		}
		cmd = exec.Command(path, title, text)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// alertsText says how hive tells the user an agent needs them.
func alertsText(a adapters.Alerts) string {
	var ways []string
	if a.Tmux {
		ways = append(ways, "a tmux message")
	}
	if a.Desktop {
		ways = append(ways, "a desktop notification")
	}
	if len(ways) == 0 {
		return "no alert ([alerts] in config.toml)"
	}
	return strings.Join(ways, " and ")
}

// label names a session in messages.
func label(s store.Session) string { return s.Tool + " ‹" + truncate(title(s), 40) + "›" }
