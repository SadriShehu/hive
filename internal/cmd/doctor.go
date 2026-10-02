package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tmux"
)

// checkup prints one line per check and counts the problems.
type checkup struct {
	color    bool
	problems int
}

func (c *checkup) ok(what string) { fmt.Println(paint("✓", "32", c.color), what) }

func (c *checkup) skip(what string) {
	fmt.Println(paint("·", "2", c.color), paint(what, "2", c.color))
}

func (c *checkup) warn(what, fix string) {
	fmt.Println(paint("!", "33", c.color), what+paint(" — "+fix, "2", c.color))
}

func (c *checkup) fail(what, fix string) {
	c.problems++
	fmt.Println(paint("✗", "31;1", c.color), what+paint(" — "+fix, "2", c.color))
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that hive, tmux and every agent's hooks are set up",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := &checkup{color: useColor()}
			checkHive(c)
			checkTmux(c)
			checkStore(c)
			checkAgents(c)
			checkLog(c)
			if c.problems > 0 {
				return fmt.Errorf("%d problem(s)", c.problems)
			}
			return nil
		},
	}
}

func checkHive(c *checkup) {
	self, _ := os.Executable()
	onPath, err := exec.LookPath("hive")
	if err != nil {
		c.fail("hive isn't on PATH", "agents can't run `hive new`; add "+shortPath(filepath.Dir(self))+" to PATH")
		return
	}
	c.ok("hive " + shortPath(onPath))
}

func checkTmux(c *checkup) {
	if !tmux.Available() {
		c.fail("tmux isn't on PATH", "hive opens every agent in tmux; install it")
		return
	}
	version, _ := exec.Command("tmux", "-V").Output()
	c.ok(strings.TrimSpace(string(version)))
	if key := tmux.PopupKey(tmux.ConfPath()); key != "" {
		c.ok("prefix+" + key + " opens the tree")
	} else {
		c.warn("no tmux key for the tree", "`hive install tmux`")
	}
	if key := tmux.NextKey(tmux.ConfPath()); key != "" {
		c.ok("prefix+" + key + " jumps to the agent that needs you")
	} else {
		c.warn("no tmux key for the agent that needs you", "`hive install tmux`")
	}
	if inStatusLine() {
		c.ok("tmux's status line counts the agents that need you")
	} else {
		c.skip("tmux's status line doesn't show `hive status` (optional; see `hive status --help`)")
	}
	// A config with mistakes is reported with the agents, below.
	if alerts, err := adapters.LoadAlerts(paths.ConfigPath()); err == nil {
		c.ok("when an agent needs you: " + alertsText(alerts))
	}
}

func checkStore(c *checkup) {
	st, err := store.Open(paths.DBPath())
	if err != nil {
		c.fail("database "+shortPath(paths.DBPath()), err.Error())
		return
	}
	defer st.Close()
	sessions, err := st.All()
	if err != nil {
		c.fail("database "+shortPath(paths.DBPath()), err.Error())
		return
	}
	c.ok(fmt.Sprintf("database %s: %d sessions", shortPath(paths.DBPath()), len(sessions)))
}

func checkAgents(c *checkup) {
	all, err := adapters.Load(paths.ConfigPath())
	switch {
	case err != nil:
		c.fail("config ignored", err.Error())
	case exists(paths.ConfigPath()):
		c.ok("config " + shortPath(paths.ConfigPath()))
	}
	for _, a := range all {
		spec := a.Spec()
		program := spec.Name
		if len(spec.New) > 0 {
			program = spec.New[0]
		}
		switch _, err := exec.LookPath(program); {
		case err != nil:
			c.skip(spec.Name + " isn't installed")
		case !a.Installed():
			c.fail(spec.Name+" isn't connected", "`hive install "+spec.Name+"`")
		default:
			c.ok(spec.Name + " connected")
		}
	}
}

// checkLog reports what hooks logged in the last day: they never print, so
// their errors land there.
func checkLog(c *checkup) {
	f, err := os.Open(paths.LogPath())
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		c.warn("can't read "+shortPath(paths.LogPath()), err.Error())
		return
	}
	defer f.Close()
	since := time.Now().Add(-24 * time.Hour)
	var count int
	var last string
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		stamp, _, _ := strings.Cut(scan.Text(), " ")
		if at, err := time.Parse(time.RFC3339, stamp); err == nil && at.After(since) {
			count++
			last = scan.Text()
		}
	}
	if count > 0 {
		c.warn(fmt.Sprintf("%d log lines in the last day in %s", count, shortPath(paths.LogPath())),
			"latest: "+truncate(last, 120))
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
