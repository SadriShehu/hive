package tui

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tmux"
	"github.com/sadrishehu/hive/internal/tracker"
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
	Copy(text string) error
	Tools() []string // tools that can be started here
}

type liveOps struct {
	st       *store.Store
	adapters []agent.Adapter

	mu        sync.Mutex
	refresher *tracker.Tracker // long-lived: it remembers process folders
}

// NewLiveOps returns the Ops that act on this machine.
func NewLiveOps(st *store.Store, adapters []agent.Adapter) Ops {
	return &liveOps{st: st, adapters: adapters, refresher: tracker.New(st, adapters, &tracker.System{})}
}

// tracker returns a tracker with a fresh view of processes and panes.
func (o *liveOps) tracker() *tracker.Tracker {
	return tracker.New(o.st, o.adapters, &tracker.System{})
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

func (o *liveOps) Launch(opts tracker.LaunchOptions) (tracker.Launched, error) {
	return o.tracker().Launch(opts)
}

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

func (o *liveOps) Tools() []string {
	var out []string
	for _, a := range o.adapters {
		if spec := a.Spec(); len(spec.New) > 0 {
			if _, err := exec.LookPath(spec.New[0]); err == nil {
				out = append(out, spec.Name)
			}
		}
	}
	return out
}
