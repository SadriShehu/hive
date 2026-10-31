package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/fusion"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tmux"
	"github.com/sadrishehu/hive/internal/tracker"
	"github.com/sadrishehu/hive/internal/usage"
)

// Ops is everything the TUI asks of the outside world. liveOps drives the
// tracker and tmux; tests substitute a fake.
type Ops interface {
	Refresh() ([]store.Session, error)
	Sync(ctx context.Context) error
	Tail(s store.Session, n int) ([]agent.Line, error)
	Capture(pane string) (string, error)
	Focus(pane string) error
	Launch(o tracker.LaunchOptions) (tracker.Launched, error)
	CanResume(s store.Session) error
	Resume(s store.Session, prompt string) (tracker.Launched, error)
	Send(s store.Session, text string) (string, error)
	Stop(s store.Session) error
	Trash(s store.Session) ([]store.Session, error)
	Trashed() ([]store.Session, error)
	Restore(s store.Session) ([]store.Session, error)
	Purge(s store.Session) (tracker.Purged, error)
	Copy(text string) error
	Tools() []string                      // tools that can be started here
	Models(tool string) ([]string, error) // what tool can run, as it names them; may take a second
	Fusion() fusion.Settings              // what auto means
	Usages() (map[string]store.Usage, error)
	Usage(ctx context.Context, s store.Session) (store.Usage, error)

	// Teams. The user writes to a team as "you", through the same mailbox
	// the members use.
	Teams() (TeamSnapshot, error)
	TeamFeed(teamID string, n int) ([]store.Message, error)
	TeamSay(teamID, to, text string) (string, error) // to: a role or "all"; says what happened to each
	TeamPause(teamID string, paused bool) error
	TeamLead(teamID, role string) error // "" for the user
	TeamStop(teamID string) (int, error)
	TeamStart(o tracker.TeamOptions) (string, error)
	TeamRead(teamID string, upTo int64) error
}

// TeamSnapshot is every team, who is on them, and how many channel messages
// the user hasn't read.
type TeamSnapshot struct {
	Teams   []store.Team
	Members []store.Member
	Unread  map[string]int
}

type liveOps struct {
	st       *store.Store
	adapters []agent.Adapter
	models   usage.Catalog
	fusion   fusion.Settings

	mu        sync.Mutex
	refresher *tracker.Tracker // long-lived: it remembers process folders
}

// NewLiveOps returns the Ops that act on this machine.
func NewLiveOps(st *store.Store, adapters []agent.Adapter, models usage.Catalog, settings fusion.Settings) Ops {
	return &liveOps{st: st, adapters: adapters, models: models, fusion: settings,
		refresher: tracker.New(st, adapters, &tracker.System{})}
}

// tracker returns a tracker with a fresh view of processes and panes.
func (o *liveOps) tracker() *tracker.Tracker {
	tr := tracker.New(o.st, o.adapters, &tracker.System{})
	tr.Models = o.models
	tr.Fusion = o.fusion
	return tr
}

func (o *liveOps) Usages() (map[string]store.Usage, error) { return o.tracker().AllUsage() }

func (o *liveOps) Usage(ctx context.Context, s store.Session) (store.Usage, error) {
	return o.tracker().Usage(ctx, s)
}

func (o *liveOps) Refresh() ([]store.Session, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.refresher.World = &tracker.System{}
	return o.refresher.Refresh()
}

func (o *liveOps) Sync(ctx context.Context) error {
	_, err := o.tracker().Sync(ctx)
	return err
}

func (o *liveOps) Tail(s store.Session, n int) ([]agent.Line, error) { return o.tracker().Tail(s, n) }
func (o *liveOps) Capture(pane string) (string, error)               { return tmux.Capture(pane) }
func (o *liveOps) Focus(pane string) error                           { return tmux.Focus(pane) }
func (o *liveOps) CanResume(s store.Session) error                   { return o.tracker().CanResume(s) }
func (o *liveOps) Stop(s store.Session) error                        { return o.tracker().Stop(s) }
func (o *liveOps) Trash(s store.Session) ([]store.Session, error)    { return o.tracker().Trash(s) }
func (o *liveOps) Trashed() ([]store.Session, error)                 { return o.st.Trashed() }
func (o *liveOps) Restore(s store.Session) ([]store.Session, error)  { return o.tracker().Restore(s) }

func (o *liveOps) Purge(s store.Session) (tracker.Purged, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return o.tracker().Purge(ctx, s)
}

func (o *liveOps) Launch(opts tracker.LaunchOptions) (tracker.Launched, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return o.tracker().Launch(ctx, opts)
}

func (o *liveOps) Models(tool string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return o.tracker().ToolModels(ctx, tool)
}

func (o *liveOps) Fusion() fusion.Settings { return o.fusion }

func (o *liveOps) Resume(s store.Session, prompt string) (tracker.Launched, error) {
	return o.tracker().Resume(s, prompt, tracker.LaunchOptions{})
}

func (o *liveOps) Send(s store.Session, text string) (string, error) {
	return o.tracker().Send(s, text, tracker.LaunchOptions{})
}

// Copy puts text on the tmux paste buffer and, on macOS, the clipboard.
func (o *liveOps) Copy(text string) error {
	_, err := tmux.Run("set-buffer", "--", text)
	if runtime.GOOS == "darwin" {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		err = cmd.Run()
	}
	return err
}

func (o *liveOps) Tools() []string { return o.tracker().InstalledTools() }

func (o *liveOps) Teams() (TeamSnapshot, error) {
	var snap TeamSnapshot
	var err error
	if snap.Teams, err = o.st.Teams(); err != nil {
		return snap, err
	}
	if snap.Members, err = o.st.AllMembers(); err != nil {
		return snap, err
	}
	snap.Unread, err = o.st.Unread()
	return snap, err
}

func (o *liveOps) TeamFeed(teamID string, n int) ([]store.Message, error) {
	return o.st.Messages(teamID, n, false)
}

func (o *liveOps) TeamSay(teamID, to, text string) (string, error) {
	tm, err := o.st.TeamByID(teamID)
	if err != nil {
		return "", err
	}
	_, sent, err := o.tracker().Say(tm, store.FromYou, to, text, false)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, s := range sent {
		parts = append(parts, s.Role+": "+s.How)
	}
	return strings.Join(parts, " · "), nil
}

func (o *liveOps) TeamPause(teamID string, paused bool) error {
	tm, err := o.st.TeamByID(teamID)
	if err != nil {
		return err
	}
	return o.tracker().SetPaused(tm, paused)
}

func (o *liveOps) TeamLead(teamID, role string) error {
	tm, err := o.st.TeamByID(teamID)
	if err != nil {
		return err
	}
	return o.tracker().SetLead(tm, role)
}

func (o *liveOps) TeamStop(teamID string) (int, error) {
	tm, err := o.st.TeamByID(teamID)
	if err != nil {
		return 0, err
	}
	return o.tracker().StopTeam(tm)
}

func (o *liveOps) TeamStart(opts tracker.TeamOptions) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tm, launched, err := o.tracker().NewTeam(ctx, opts)
	if err != nil {
		return "", err
	}
	started, failed := 0, ""
	for _, l := range launched {
		if l.Err == nil {
			started++
		} else if failed == "" {
			failed = l.Member.Role + ": " + l.Err.Error()
		}
	}
	text := fmt.Sprintf("team %s: %d of %d members started", tm.Name, started, len(launched))
	if failed != "" {
		return text, errors.New(text + "; " + failed)
	}
	return text, nil
}

func (o *liveOps) TeamRead(teamID string, upTo int64) error { return o.st.MarkRead(teamID, upTo) }
