// Package tracker turns agent reports into the session tree. It links each
// session to its parent, finds the tmux pane it runs in, and keeps liveness
// current. Nothing here is specific to one tool, so any adapter can be the
// parent or the child of any other.
package tracker

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/proc"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tmux"
	"github.com/sadrishehu/hive/internal/usage"
)

// ParentEnv is the variable every agent sets, for the commands it runs, to
// its own session ID. It survives nohup, setsid and anything else that cuts
// the process tree.
const ParentEnv = "HIVE_PARENT"

// launchTTL bounds how long a launch record waits for its agent to report in.
const launchTTL = 10 * time.Minute

// World is what the tracker observes about the machine. System is the real
// one; tests substitute their own.
type World interface {
	Procs() proc.Table
	Panes() []tmux.Pane
	Cwd(pid int) string
	Getenv(key string) string
	SelfPID() int
	Now() int64 // epoch ms

	// Forget drops what was read so far, so the next look is fresh.
	Forget()
}

// System is the World as seen from this process. The process table and pane
// list are read once, on first use.
type System struct {
	procs    proc.Table
	panes    []tmux.Pane
	hasProcs bool
	hasPanes bool
}

func (w *System) Procs() proc.Table {
	if !w.hasProcs {
		w.procs, _ = proc.Snapshot()
		w.hasProcs = true
	}
	return w.procs
}

func (w *System) Panes() []tmux.Pane {
	if !w.hasPanes {
		w.panes, _ = tmux.Panes()
		w.hasPanes = true
	}
	return w.panes
}

func (w *System) Forget() { *w = System{} }

func (w *System) Cwd(pid int) string       { return proc.Cwd(pid) }
func (w *System) Getenv(key string) string { return os.Getenv(key) }
func (w *System) SelfPID() int             { return os.Getpid() }
func (w *System) Now() int64               { return time.Now().UnixMilli() }

// Tracker records events and answers what is running.
type Tracker struct {
	Store    *store.Store
	Adapters []agent.Adapter
	World    World

	// Logf, when set, receives one line per decision, for debugging links.
	Logf func(format string, args ...any)

	Models usage.Catalog

	cwds map[string]string // process folders, by pid@start
}

// New returns a tracker.
func New(st *store.Store, adapters []agent.Adapter, w World) *Tracker {
	return &Tracker{Store: st, Adapters: adapters, World: w}
}

func (t *Tracker) logf(format string, args ...any) {
	if t.Logf != nil {
		t.Logf(format, args...)
	}
}

func (t *Tracker) spec(tool string) agent.Spec {
	for _, a := range t.Adapters {
		if a.Spec().Name == tool {
			return a.Spec()
		}
	}
	return agent.Spec{Name: tool}
}

// toolOf returns the adapter whose process p is.
func (t *Tracker) toolOf(p proc.Proc) (agent.Spec, bool) {
	for _, a := range t.Adapters {
		if s := a.Spec(); s.Matches(p) {
			return s, true
		}
	}
	return agent.Spec{}, false
}

// Ingest records one event.
func (t *Tracker) Ingest(ev agent.Event) error {
	if ev.SessionID == "" {
		return fmt.Errorf("%s event without a session ID", ev.Tool)
	}
	id := store.ID(ev.Tool, ev.SessionID)
	at := ev.At
	if at == 0 {
		at = t.World.Now()
	}
	cur, exists, err := t.Store.Get(id)
	if err != nil {
		return err
	}
	if !exists && ev.Type == agent.End {
		return nil // never saw it start: nothing to end
	}
	if exists && cur.DeletedAt > 0 && ev.Type == agent.End {
		return nil // a late end doesn't bring a session back from the trash
	}

	s := store.Session{
		ID: id, Tool: ev.Tool, NativeID: ev.SessionID,
		Title: ev.Title, Cwd: ev.Cwd, Transcript: ev.Transcript, LastPrompt: ev.Prompt,
		CreatedAt: at, UpdatedAt: at, Source: store.SourceHook,
	}
	// Status events for a subagent don't repeat that it is one.
	internal := ev.Internal || (exists && cur.Kind == store.KindInternal)
	if internal {
		s.Kind = store.KindInternal
	}

	// Find the process and pane when the session is new, reopened, or moved.
	attach := ev.Type != agent.End && !internal &&
		(!exists || cur.PID == 0 || ev.Type == agent.Start || (ev.PID > 0 && ev.PID != cur.PID))
	if exists && cur.Status == store.StatusExited && at < cur.StatusAt {
		attach = false // a late event from before the session ended
	}
	var pid int
	var pane string
	if attach {
		pid = ev.PID
		if pid == 0 {
			pid = t.agentPID(t.spec(ev.Tool))
		}
		if pid == 0 {
			pid = t.locateProcess(t.spec(ev.Tool), ev.Cwd)
		}
		if pid > 0 {
			var nested bool
			pane, nested = t.paneOf(pid)
			s.Kind = store.KindInteractive
			if nested || ev.Headless || t.spec(ev.Tool).IsHeadless(t.World.Procs()[pid].Argv()) {
				s.Kind = store.KindHeadless
			}
		}
	}

	var via string
	if !exists || cur.ParentID == "" {
		s.ParentID, via = t.parentOf(id, ev, pid, pane)
		if s.ParentID != "" && t.cycles(id, s.ParentID) {
			t.logf("%s: ignoring parent %s, it would make a cycle", id, s.ParentID)
			s.ParentID = ""
		}
	}
	msg := fmt.Sprintf("%s %s", ev.Type, id)
	if attach {
		msg += fmt.Sprintf(" pid=%d pane=%q kind=%s", pid, pane, s.Kind)
	}
	if via != "" {
		msg += fmt.Sprintf(" parent=%q via=%s", s.ParentID, via)
	}
	t.logf("%s", msg)

	if err := t.Store.Upsert(s); err != nil {
		return err
	}
	if strings.HasPrefix(via, "env") {
		if err := t.adopt(s.ParentID, pid, at); err != nil {
			return err
		}
	}
	if attach && pid > 0 {
		if err := t.Store.Attach(id, pid, pane, s.Kind); err != nil {
			return err
		}
		if err := t.Store.EndOthersOnPID(pid, id, at); err != nil {
			return err
		}
	}
	if status := statusOf(ev.Type); status != "" {
		if err := t.Store.SetStatus(id, status, at); err != nil {
			return err
		}
	}
	if ev.Type == agent.Start && ev.EnvFile != "" {
		return exportParent(ev.EnvFile, id)
	}
	return nil
}

func statusOf(e agent.EventType) string {
	switch e {
	case agent.Start, agent.Idle:
		return store.StatusIdle
	case agent.Prompt, agent.Busy:
		return store.StatusWorking
	case agent.Attention:
		return store.StatusAttention
	case agent.End:
		return store.StatusExited
	}
	return ""
}

// agentPID walks up from the hook process to the nearest process of the tool.
func (t *Tracker) agentPID(spec agent.Spec) int {
	procs := t.World.Procs()
	for _, a := range procs.Ancestors(t.World.SelfPID()) {
		if spec.Matches(procs[a]) {
			return a
		}
	}
	return 0
}

func (t *Tracker) locateProcess(spec agent.Spec, cwd string) int {
	if cwd == "" {
		return 0
	}
	if pid := t.launchedProcessIn(spec, cwd); pid > 0 {
		return pid
	}
	sessions, err := t.Store.All()
	if err != nil {
		return 0
	}
	var found []int
	for _, p := range t.orphans(sessions) {
		if spec.Matches(p) && t.cwd(p) == cwd {
			found = append(found, p.PID)
		}
	}
	return onlyOne(found)
}

func (t *Tracker) launchedProcessIn(spec agent.Spec, cwd string) int {
	launches, err := t.Store.Launches()
	if err != nil {
		return 0
	}
	byPane := map[string]int{}
	for _, p := range t.World.Panes() {
		byPane[p.ID] = p.PID
	}
	procs := t.World.Procs()
	since := t.World.Now() - launchTTL.Milliseconds()
	var found []int
	for pane, l := range launches {
		pid := byPane[pane]
		if l.Tool == spec.Name && l.Cwd == cwd && l.CreatedAt >= since && spec.Matches(procs[pid]) {
			found = append(found, pid)
		}
	}
	return onlyOne(found)
}

func onlyOne(pids []int) int {
	if len(pids) != 1 {
		return 0
	}
	return pids[0]
}

// paneOf walks up from pid to the tmux pane hosting it. Meeting another
// agent's process first means pid runs inside that agent (its shell tool),
// so it has no pane of its own.
func (t *Tracker) paneOf(pid int) (pane string, nested bool) {
	byPID := map[int]string{}
	for _, p := range t.World.Panes() {
		byPID[p.PID] = p.ID
	}
	procs := t.World.Procs()
	self, _ := t.toolOf(procs[pid])
	cur := pid
	for i := 0; i < 128 && cur > 1; i++ {
		if id, ok := byPID[cur]; ok {
			return id, false
		}
		p, ok := procs[cur]
		if !ok {
			return "", false
		}
		if parent, ok := procs[p.PPID]; ok {
			if spec, isAgent := t.toolOf(parent); isAgent {
				// A same-tool parent is the tool's own launcher or worker,
				// not a separate session.
				if cur != pid || spec.Name != self.Name {
					return "", true
				}
			}
		}
		cur = p.PPID
	}
	return "", false
}

// parentOf resolves a session's parent. The first match wins:
//  1. the parent the event names;
//  2. the launch record hive made for the session's tmux pane;
//  3. the nearest ancestor process that is a live session;
//  4. HIVE_PARENT, which every agent sets for the commands it runs;
//  5. an adapter's own variable, like CLAUDE_CODE_SESSION_ID.
//
// Ancestry comes before the environment because children inherit variables
// from further up: a Claude run inside opencode inside Claude still sees the
// outer CLAUDE_CODE_SESSION_ID.
func (t *Tracker) parentOf(id string, ev agent.Event, pid int, pane string) (string, string) {
	ok := func(p string) bool { return p != "" && p != id }
	if ok(ev.ParentID) {
		return ev.ParentID, "event"
	}
	if pane != "" {
		since := t.World.Now() - launchTTL.Milliseconds()
		if l, fresh, err := t.Store.TakeLaunch(pane, since); err == nil && fresh && ok(l.ParentID) {
			return l.ParentID, "launch"
		}
	}
	return t.inherited(id, pid)
}

// Caller is the session running the command that runs hive, found the way a
// new session's parent is (steps 3–5 of parentOf), or "" outside any agent.
func (t *Tracker) Caller() string {
	id, _ := t.inherited("", t.World.SelfPID())
	return id
}

// inherited finds id's parent from pid's ancestors and the environment.
func (t *Tracker) inherited(id string, pid int) (string, string) {
	ok := func(p string) bool { return p != "" && p != id }
	if pid > 0 {
		for _, a := range t.World.Procs().Ancestors(pid) {
			sessions, err := t.Store.OnPID(a)
			if err != nil {
				break
			}
			for _, s := range sessions {
				if ok(s.ID) {
					return s.ID, "process"
				}
			}
		}
	}
	if p := t.World.Getenv(ParentEnv); ok(p) {
		return p, "env " + ParentEnv
	}
	for _, a := range t.Adapters {
		spec := a.Spec()
		if spec.ParentEnv == "" {
			continue
		}
		if v := t.World.Getenv(spec.ParentEnv); v != "" && ok(store.ID(spec.Name, v)) {
			return store.ID(spec.Name, v), "env " + spec.ParentEnv
		}
	}
	return "", "none"
}

// adopt gives a parent known only by name, from the environment, its process:
// the child's nearest ancestor running the parent's tool. Sessions that were
// running before hive was installed become tracked as soon as they spawn
// something.
func (t *Tracker) adopt(parentID string, childPID int, at int64) error {
	tool, native, ok := strings.Cut(parentID, ":")
	if !ok || childPID == 0 {
		return nil
	}
	cur, exists, err := t.Store.Get(parentID)
	if err != nil || (exists && cur.PID > 0) {
		return err
	}
	spec := t.spec(tool)
	procs := t.World.Procs()
	for _, a := range procs.Ancestors(childPID) {
		if !spec.Matches(procs[a]) {
			continue
		}
		if others, err := t.Store.OnPID(a); err != nil || len(others) > 0 {
			return err // that process has a session of its own: the variable is stale
		}
		pane, nested := t.paneOf(a)
		kind := store.KindInteractive
		if nested || spec.IsHeadless(procs[a].Argv()) {
			kind = store.KindHeadless
		}
		if err := t.Store.Upsert(store.Session{ID: parentID, Tool: tool, NativeID: native,
			Cwd: t.World.Cwd(a), Kind: kind, CreatedAt: at, UpdatedAt: at, Source: store.SourceInferred}); err != nil {
			return err
		}
		if err := t.Store.Attach(parentID, a, pane, kind); err != nil {
			return err
		}
		// Its status stays unknown: without hooks, nothing would keep it current.
		t.logf("adopt %s pid=%d pane=%q kind=%s", parentID, a, pane, kind)
		return nil
	}
	return nil
}

// cycles reports whether making parent the parent of id would loop.
func (t *Tracker) cycles(id, parent string) bool {
	cur := parent
	for range 64 {
		if cur == id {
			return true
		}
		s, ok, err := t.Store.Get(cur)
		if err != nil || !ok || s.ParentID == "" {
			return false
		}
		cur = s.ParentID
	}
	return true
}

// exportParent makes HIVE_PARENT reach everything the agent's shell runs.
func exportParent(envFile, id string) error {
	f, err := os.OpenFile(envFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "export %s=%s\n", ParentEnv, agent.ShellQuote(id))
	return err
}

// Refresh reconciles the database with the running processes and returns
// every session, plus agent processes no session claims ("untracked").
func (t *Tracker) Refresh() ([]store.Session, error) {
	sessions, err := t.Store.All()
	if err != nil {
		return nil, err
	}
	procs := t.World.Procs()
	now := t.World.Now()
	panes := map[string]bool{}
	for _, p := range t.World.Panes() {
		panes[p.ID] = true
	}

	for i, s := range sessions {
		if s.PID == 0 {
			continue
		}
		p, running := procs[s.PID]
		if !running || !t.spec(s.Tool).Matches(p) {
			if err := t.Store.MarkGone(s.ID, now); err != nil {
				return nil, err
			}
			sessions[i].Status, sessions[i].PID, sessions[i].Pane = store.StatusExited, 0, ""
			continue
		}
	}

	// A tool's own subagents end with the session that runs them.
	byID := map[string]*store.Session{}
	for i := range sessions {
		byID[sessions[i].ID] = &sessions[i]
	}
	for i, s := range sessions {
		if s.Kind != store.KindInternal || !s.Live() {
			continue
		}
		if parent := byID[s.ParentID]; parent == nil || !parent.Live() {
			if err := t.Store.MarkGone(s.ID, now); err != nil {
				return nil, err
			}
			sessions[i].Status = store.StatusExited
		}
	}

	claimed, err := t.claim(sessions)
	if err != nil {
		return nil, err
	}
	if claimed {
		if sessions, err = t.Store.All(); err != nil {
			return nil, err
		}
	}
	// A pane this server doesn't have may belong to another tmux server, so
	// it is hidden from the result, not forgotten.
	for i := range sessions {
		if sessions[i].Pane != "" && !panes[sessions[i].Pane] {
			sessions[i].Pane = ""
		}
	}
	return t.withoutPrewarmed(append(sessions, t.untracked(sessions)...), procs), nil
}

// withoutPrewarmed drops sessions a tool may have started ahead of use (see
// agent.Prewarmer) while nothing shows anyone has used them: no title, no
// message, no children. The database keeps them, so they appear with their
// first message.
func (t *Tracker) withoutPrewarmed(sessions []store.Session, procs proc.Table) []store.Session {
	parents := map[string]bool{}
	for _, s := range sessions {
		parents[s.ParentID] = true
	}
	return slices.DeleteFunc(sessions, func(s store.Session) bool {
		if !s.Live() || s.PID <= 0 || s.Title != "" || s.LastPrompt != "" || parents[s.ID] {
			return false
		}
		w, ok := t.adapter(s.Tool).(agent.Prewarmer)
		if !ok {
			return false
		}
		chain := []proc.Proc{procs[s.PID]}
		for _, pid := range procs.Ancestors(s.PID) {
			chain = append(chain, procs[pid])
		}
		return w.Prewarmed(chain)
	})
}

// orphans returns agent processes that no session claims: agents started
// before hive was installed, or tools without hooks.
func (t *Tracker) orphans(sessions []store.Session) []proc.Proc {
	claimed := map[int]bool{}
	for _, s := range sessions {
		if s.PID > 0 {
			claimed[s.PID] = true
		}
	}
	procs := t.World.Procs()
	var out []proc.Proc
	for pid, p := range procs {
		spec, ok := t.toolOf(p)
		if !ok || claimed[pid] {
			continue
		}
		if parent, ok := procs[p.PPID]; ok && spec.Matches(parent) {
			continue // the tool's own worker process
		}
		out = append(out, p)
	}
	return out
}

// claim matches orphan processes to the sessions they run, when the tool's
// own history makes that unambiguous: the process is the only orphan of its
// tool in its folder, and exactly one session of that tool in that folder
// was active since the process started.
func (t *Tracker) claim(sessions []store.Session) (bool, error) {
	type place struct{ tool, cwd string }
	groups := map[place][]proc.Proc{}
	for _, p := range t.orphans(sessions) {
		spec, _ := t.toolOf(p)
		if cwd := t.cwd(p); cwd != "" && p.Started > 0 {
			groups[place{spec.Name, cwd}] = append(groups[place{spec.Name, cwd}], p)
		}
	}
	claimed := false
	for at, ps := range groups {
		if len(ps) != 1 {
			continue
		}
		p := ps[0]
		candidates, err := t.Store.ClaimCandidates(at.tool, at.cwd, p.Started)
		if err != nil {
			return claimed, err
		}
		if len(candidates) != 1 {
			continue
		}
		id := candidates[0].ID
		pane, kind := t.placeOf(p, t.spec(at.tool))
		if err := t.Store.Attach(id, p.PID, pane, kind); err != nil {
			return claimed, err
		}
		if candidates[0].Status == store.StatusExited {
			if err := t.Store.SetStatus(id, store.StatusUnknown, t.World.Now()); err != nil {
				return claimed, err
			}
		}
		if err := t.adoptLaunchParent(id, pane); err != nil {
			return claimed, err
		}
		t.logf("claim %s pid=%d pane=%q", id, p.PID, pane)
		claimed = true
	}
	return claimed, nil
}

func (t *Tracker) adoptLaunchParent(id, pane string) error {
	if pane == "" {
		return nil
	}
	since := t.World.Now() - launchTTL.Milliseconds()
	l, fresh, err := t.Store.TakeLaunch(pane, since)
	if err != nil || !fresh || l.ParentID == "" || l.ParentID == id || t.cycles(id, l.ParentID) {
		return err
	}
	_, err = t.Store.SetParent(id, l.ParentID)
	return err
}

// placeOf returns the pane a process runs in and whether it is interactive.
func (t *Tracker) placeOf(p proc.Proc, spec agent.Spec) (pane, kind string) {
	pane, nested := t.paneOf(p.PID)
	kind = store.KindInteractive
	if nested || spec.IsHeadless(p.Argv()) {
		kind = store.KindHeadless
	}
	return pane, kind
}

// cwd returns a process's folder, remembered per process: reading it is slow.
func (t *Tracker) cwd(p proc.Proc) string {
	key := fmt.Sprintf("%d@%d", p.PID, p.Started)
	if dir, ok := t.cwds[key]; ok {
		return dir
	}
	if t.cwds == nil {
		t.cwds = map[string]string{}
	}
	dir := t.World.Cwd(p.PID)
	t.cwds[key] = dir
	return dir
}

// untracked reports orphan processes as sessions without an ID. One that
// hive launched and that hasn't reported in yet (opencode names its session
// only at the first message) shows where hive put it, as source "pending".
func (t *Tracker) untracked(sessions []store.Session) []store.Session {
	procs := t.World.Procs()
	launches, _ := t.Store.Launches()
	var out []store.Session
	for _, p := range t.orphans(sessions) {
		spec, _ := t.toolOf(p)
		pane, kind := t.placeOf(p, spec)
		native := "pid-" + strconv.Itoa(p.PID)
		s := store.Session{
			ID: store.ID(spec.Name, native), Tool: spec.Name, NativeID: native,
			Cwd: t.cwd(p), Kind: kind, Status: store.StatusUnknown,
			PID: p.PID, Pane: pane, Source: "scan", CreatedAt: p.Started, UpdatedAt: p.Started,
		}
		if l, ok := launches[pane]; ok && pane != "" && l.Tool == spec.Name {
			s.ParentID, s.Source = l.ParentID, "pending"
		} else {
			for _, a := range procs.Ancestors(p.PID) {
				if ss, err := t.Store.OnPID(a); err == nil && len(ss) > 0 {
					s.ParentID = ss[0].ID
					break
				}
			}
		}
		out = append(out, s)
	}
	return out
}
