package tracker

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/fusion"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tmux"
)

// LaunchOptions describes where and how to start an agent's TUI.
type LaunchOptions struct {
	Tool     string      // a tool, or fusion.Auto to pick one for the prompt
	Model    string      // a model as the tool names it, fusion.Auto to pick one, or "" for the tool's default
	Mode     fusion.Mode // what auto means; "" takes the configured mode
	Cwd      string
	Prompt   string
	ParentID string // the session this one is a child of; "" for a top-level agent
	Session  string // tmux session for the window; "" means tmux.HomeSession
	Detached bool   // open the window without switching to it
}

// Launched is where an agent now runs, and as what.
type Launched struct {
	Pane      string
	SessionID string // known right away when the tool takes a pre-assigned ID
	Tool      string
	Model     string         // the model it was started with; "" for the tool's default
	Choice    *fusion.Choice // set when hive picked the tool or the model
}

func (t *Tracker) adapter(tool string) agent.Adapter {
	for _, a := range t.Adapters {
		if a.Spec().Name == tool {
			return a
		}
	}
	return nil
}

// Launch starts a new interactive agent in its own tmux window, picking
// the tool or the model first when asked to.
func (t *Tracker) Launch(ctx context.Context, o LaunchOptions) (Launched, error) {
	var out Launched
	if (PickOptions{Tool: o.Tool, Model: o.Model}).Wants() {
		choice, err := t.Pick(ctx, PickOptions{Tool: o.Tool, Model: o.Model, Mode: o.Mode, Prompt: o.Prompt, ParentID: o.ParentID})
		if err != nil {
			return Launched{}, err
		}
		o.Tool, o.Model = choice.Tool, choice.ID
		out.Choice = &choice
	}
	spec := t.spec(o.Tool)
	switch {
	case len(spec.New) == 0:
		return Launched{}, fmt.Errorf("hive doesn't know how to start %q", o.Tool)
	case o.Model != "" && !spec.TakesModel():
		return Launched{}, fmt.Errorf("hive can't set the model for %s: its start command has no {model}; add one in config.toml", o.Tool)
	}
	out.Tool, out.Model = o.Tool, o.Model
	cwd, err := folder(o.Cwd)
	if err != nil {
		return Launched{}, err
	}
	var native string
	if slices.ContainsFunc(spec.New, func(a string) bool { return strings.Contains(a, "{session}") }) {
		native = newUUID()
	}
	argv, err := resolve(agent.Expand(spec.New, map[string]string{"prompt": o.Prompt, "session": native, "model": o.Model}))
	if err != nil {
		return Launched{}, err
	}
	env := map[string]string{}
	if o.ParentID != "" {
		env[ParentEnv] = o.ParentID
	}
	started := t.World.Now()
	pane, err := tmux.NewWindow(tmux.Window{Session: o.Session, Name: windowName(o.Tool, cwd),
		Dir: cwd, Env: env, Argv: argv, Detached: o.Detached})
	if err != nil {
		return Launched{}, err
	}
	now := t.World.Now()
	if err := t.Store.PutLaunch(store.Launch{Pane: pane.ID, Tool: o.Tool, ParentID: o.ParentID, Cwd: cwd, CreatedAt: now}); err != nil {
		return Launched{}, err
	}
	out.Pane = pane.ID
	if o.Prompt != "" {
		// Until the agent names its session, its pane holds the prompt.
		key := pane.ID
		if native != "" {
			key = store.ID(o.Tool, native)
		}
		if err := t.Store.SetInput(key, started); err != nil {
			return out, err
		}
	}
	if native != "" {
		// The session exists before its first hook arrives.
		out.SessionID = store.ID(o.Tool, native)
		err := errors.Join(
			t.Store.Upsert(store.Session{ID: out.SessionID, Tool: o.Tool, NativeID: native, ParentID: o.ParentID,
				Cwd: cwd, Kind: store.KindInteractive, LastPrompt: o.Prompt, CreatedAt: now, UpdatedAt: now, Source: store.SourceLaunch}),
			t.Store.Attach(out.SessionID, pane.PID, pane.ID, store.KindInteractive),
			t.Store.SetStatus(out.SessionID, startStatus(o.Prompt), now),
		)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// CanResume says why s can't be reopened, or returns nil.
func (t *Tracker) CanResume(s store.Session) error {
	spec := t.spec(s.Tool)
	switch {
	case s.Synthetic():
		return errors.New("hive doesn't know which session this process runs yet")
	case s.Live():
		return errors.New("it is already running")
	case len(spec.Resume) == 0:
		return fmt.Errorf("hive doesn't know how to reopen %s sessions", s.Tool)
	case s.Kind == store.KindInternal && !spec.ResumeSubagents:
		return fmt.Errorf("%s subagents can't be reopened on their own; open the parent", s.Tool)
	}
	if checker, ok := t.adapter(s.Tool).(agent.ResumeChecker); ok {
		return checker.CheckResume(s)
	}
	return nil
}

// Resume reopens s in the tool's own TUI, in a new tmux window, with prompt
// as its next message if given.
func (t *Tracker) Resume(s store.Session, prompt string, o LaunchOptions) (Launched, error) {
	if err := t.CanResume(s); err != nil {
		return Launched{}, err
	}
	argv, err := resolve(agent.Expand(t.spec(s.Tool).Resume, map[string]string{"id": s.NativeID, "prompt": prompt}))
	if err != nil {
		return Launched{}, err
	}
	cwd, err := folder(s.Cwd)
	if err != nil {
		cwd = paths.Home() // the project folder is gone; the tool will say what it can
	}
	started := t.World.Now()
	pane, err := tmux.NewWindow(tmux.Window{Session: o.Session, Name: windowName(s.Tool, cwd),
		Dir: cwd, Argv: argv, Detached: o.Detached})
	if err != nil {
		return Launched{}, err
	}
	now := t.World.Now()
	input := t.Store.ClearInput(s.ID) // whatever the last run left unanswered
	if prompt != "" {
		input = t.Store.SetInput(s.ID, started)
	}
	err = errors.Join(
		// If the tool continues under a new session ID, it lands in the same place.
		t.Store.PutLaunch(store.Launch{Pane: pane.ID, Tool: s.Tool, ParentID: s.ParentID, Cwd: cwd, CreatedAt: now}),
		t.Store.Attach(s.ID, pane.PID, pane.ID, store.KindInteractive),
		t.Store.SetStatus(s.ID, startStatus(prompt), now),
		input,
	)
	return Launched{Pane: pane.ID, SessionID: s.ID}, err
}

// Send gives s a message: typed into its pane when it runs in one, or by
// reopening it with the message.
func (t *Tracker) Send(s store.Session, text string, o LaunchOptions) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", errors.New("nothing to send")
	}
	if s.Pane != "" && tmux.PaneExists(s.Pane) {
		// Recorded before typing: the agent may answer before Paste returns.
		key := inputKey(s)
		if err := t.Store.SetInput(key, t.World.Now()); err != nil {
			return "", err
		}
		if err := tmux.Paste(s.Pane, text); err != nil {
			t.Store.ClearInput(key)
			return "", err
		}
		return "sent", nil
	}
	if s.Live() {
		switch s.Kind {
		case store.KindInternal:
			return "", errors.New("a subagent takes input only through its parent")
		case store.KindHeadless:
			return "", fmt.Errorf("it's a headless run (pid %d) and can't take input; send once it finishes", s.PID)
		}
		return "", fmt.Errorf("it runs outside tmux (pid %d), where hive can't type", s.PID)
	}
	if _, err := t.Resume(s, text, o); err != nil {
		return "", err
	}
	return "reopened with your message", nil
}

// Stop ends s's process. When the agent is all its pane runs (as in every
// window hive opens), the pane closes with it; under someone's shell, only
// the agent is asked to stop and the shell stays.
func (t *Tracker) Stop(s store.Session) error {
	if s.PID <= 0 {
		return errors.New("it isn't running as a process of its own")
	}
	// Check the pid still belongs to the tool before signalling it.
	if p, ok := t.World.Procs()[s.PID]; !ok || !t.spec(s.Tool).Matches(p) {
		return errors.New("its process is already gone")
	}
	for _, p := range t.World.Panes() {
		if p.ID == s.Pane && p.PID == s.PID {
			_, err := tmux.Run("kill-pane", "-t", p.ID)
			return err
		}
	}
	return syscall.Kill(s.PID, syscall.SIGTERM)
}

// Purged is what Purge deleted.
type Purged struct {
	Sessions []store.Session // gone from hive, and from their tools
	Kept     []string        // tools hive can't delete sessions from: their own copies stay
}

// Trash moves s and everything under it to the trash, once nothing in there
// runs. The tools keep their copies, and Restore brings it all back.
func (t *Tracker) Trash(s store.Session) ([]store.Session, error) {
	if s.Synthetic() {
		return nil, errors.New("hive doesn't know which session this process runs yet")
	}
	family, err := t.idle(s.ID, false)
	if err != nil {
		return nil, err
	}
	return family, t.Store.Trash(sessionIDs(family), t.World.Now())
}

// Restore takes s and everything under it in the trash out of the trash.
func (t *Tracker) Restore(s store.Session) ([]store.Session, error) {
	family, err := t.Store.Family(s.ID, true)
	if err != nil {
		return nil, err
	}
	if len(family) == 0 {
		return nil, fmt.Errorf("%s isn't in the trash", s.ID)
	}
	return family, t.Store.Restore(sessionIDs(family))
}

// Purge deletes s and everything under it in the trash for good: from each
// tool's own storage, then from hive. It goes deepest first and stops at the
// first session its tool fails to delete, which stays in the trash, and so
// does everything above it.
func (t *Tracker) Purge(ctx context.Context, s store.Session) (Purged, error) {
	var out Purged
	family, err := t.idle(s.ID, true)
	if err != nil {
		return out, err
	}
	for _, m := range deepestFirst(family) {
		if p, ok := t.adapter(m.Tool).(agent.Purger); ok {
			if err := p.Purge(ctx, m); err != nil {
				return out, fmt.Errorf("%s: %w", m.ID, err)
			}
		} else if !slices.Contains(out.Kept, m.Tool) {
			out.Kept = append(out.Kept, m.Tool)
		}
		if err := t.Store.Purge(m.ID, t.World.Now()); err != nil {
			return out, err
		}
		out.Sessions = append(out.Sessions, m)
	}
	return out, nil
}

// idle returns the family of id in the trash or out of it, as trashed says,
// once it is sure nothing in there runs.
func (t *Tracker) idle(id string, trashed bool) ([]store.Session, error) {
	t.World.Forget()
	if _, err := t.Refresh(); err != nil {
		return nil, err
	}
	family, err := t.Store.Family(id, trashed)
	if err != nil {
		return nil, err
	}
	switch {
	case len(family) == 0 && trashed:
		return nil, fmt.Errorf("%s isn't in the trash", id)
	case len(family) == 0:
		return nil, fmt.Errorf("%s isn't in hive's records, or is in the trash already", id)
	}
	for _, m := range family {
		if m.Live() {
			return nil, fmt.Errorf("%s is still running; stop it first", m.ID)
		}
	}
	return family, nil
}

// deepestFirst orders a family so that every session comes before its parent.
func deepestFirst(family []store.Session) []store.Session {
	byID := map[string]store.Session{}
	for _, m := range family {
		byID[m.ID] = m
	}
	depth := func(m store.Session) int {
		d := 0
		for p, ok := byID[m.ParentID]; ok && d < len(family); p, ok = byID[p.ParentID] {
			d++
		}
		return d
	}
	out := slices.Clone(family)
	slices.SortStableFunc(out, func(a, b store.Session) int { return cmp.Compare(depth(b), depth(a)) })
	return out
}

func sessionIDs(sessions []store.Session) []string {
	out := make([]string, len(sessions))
	for i, s := range sessions {
		out[i] = s.ID
	}
	return out
}

// Tail returns the end of s's transcript.
func (t *Tracker) Tail(s store.Session, n int) ([]agent.Line, error) {
	if tailer, ok := t.adapter(s.Tool).(agent.Tailer); ok {
		return tailer.Tail(s, n)
	}
	return nil, fmt.Errorf("hive can't read %s transcripts", s.Tool)
}

func startStatus(prompt string) string {
	if prompt != "" {
		return store.StatusWorking
	}
	return store.StatusIdle
}

// folder checks that dir exists, making ~ absolute.
func folder(dir string) (string, error) {
	if dir == "" {
		return paths.Home(), nil
	}
	dir = paths.Expand(dir)
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("no such folder: %s", dir)
	}
	return dir, nil
}

// resolve makes the program absolute, so the window doesn't depend on the
// tmux server's PATH.
func resolve(argv []string) ([]string, error) {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, fmt.Errorf("%s isn't on PATH", argv[0])
	}
	return append([]string{path}, argv[1:]...), nil
}

func windowName(tool, cwd string) string { return tool + "/" + filepath.Base(cwd) }

func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
