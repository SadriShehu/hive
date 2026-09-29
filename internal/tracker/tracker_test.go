package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/proc"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tmux"
)

type fakeWorld struct {
	procs proc.Table
	panes []tmux.Pane
	env   map[string]string
	self  int
	now   int64
}

func (w *fakeWorld) Procs() proc.Table        { return w.procs }
func (w *fakeWorld) Panes() []tmux.Pane       { return w.panes }
func (w *fakeWorld) Cwd(int) string           { return "/work" }
func (w *fakeWorld) Getenv(key string) string { return w.env[key] }
func (w *fakeWorld) SelfPID() int             { return w.self }
func (w *fakeWorld) Now() int64               { return w.now }

func (w *fakeWorld) add(pid, ppid int, args string) {
	w.procs[pid] = proc.Proc{PID: pid, PPID: ppid, Args: strings.Fields(args)}
}

// newWorld is a machine with one tmux pane (%1, shell 90) running Claude A
// (pid 100). A's shell tool (200) runs `opencode run` (B, pid 201), whose
// shell (300) runs `claude -p` (C, pid 301).
func newWorld(t *testing.T) (*Tracker, *fakeWorld) {
	t.Helper()
	w := &fakeWorld{procs: proc.Table{}, env: map[string]string{}, now: 1_000}
	w.add(80, 1, "tmux")
	w.add(90, 80, "-zsh")
	w.add(100, 90, "claude")
	w.add(200, 100, "/bin/zsh -c opencode run hi")
	w.add(201, 200, "opencode run -m deepseek/deepseek-flash hi")
	w.add(300, 201, "/bin/bash -c claude -p hello")
	w.add(301, 300, "claude -p hello")
	w.panes = []tmux.Pane{{ID: "%1", PID: 90}, {ID: "%2", PID: 95}}
	w.add(95, 80, "-zsh")

	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, adapters.Builtin(), w), w
}

// claudeHook ingests a Claude hook event the way Claude runs it: hive under
// a shell under the claude process.
func claudeHook(t *testing.T, tr *Tracker, w *fakeWorld, claudePID int, id string, typ agent.EventType) {
	t.Helper()
	w.add(9000+claudePID, claudePID, "/bin/sh -c hive hook claude")
	w.add(9500+claudePID, 9000+claudePID, "hive hook claude")
	w.self = 9500 + claudePID
	ingest(t, tr, agent.Event{Tool: "claude", SessionID: id, Type: typ})
}

func ingest(t *testing.T, tr *Tracker, ev agent.Event) {
	t.Helper()
	if err := tr.Ingest(ev); err != nil {
		t.Fatalf("ingest %+v: %v", ev, err)
	}
}

func get(t *testing.T, tr *Tracker, id string) store.Session {
	t.Helper()
	s, ok, err := tr.Store.Get(id)
	if err != nil || !ok {
		t.Fatalf("get %s: ok=%v err=%v", id, ok, err)
	}
	return s
}

func TestLinksAcrossToolsInEveryDirection(t *testing.T) {
	tr, w := newWorld(t)

	claudeHook(t, tr, w, 100, "A", agent.Start)
	a := get(t, tr, "claude:A")
	if a.ParentID != "" || a.PID != 100 || a.Pane != "%1" || a.Kind != store.KindInteractive {
		t.Fatalf("A = %+v, want a root, pid 100, pane %%1, interactive", a)
	}

	// Claude → opencode: B inherits HIVE_PARENT=claude:A too, but the
	// process tree decides first.
	w.env = map[string]string{ParentEnv: "claude:A", "CLAUDE_CODE_SESSION_ID": "A"}
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "B", Type: agent.Start, PID: 201})
	b := get(t, tr, "opencode:B")
	if b.ParentID != "claude:A" || b.Kind != store.KindHeadless || b.Pane != "" {
		t.Fatalf("B = %+v, want child of claude:A, headless, no pane", b)
	}

	// opencode → Claude: C still sees the outer CLAUDE_CODE_SESSION_ID=A.
	w.env = map[string]string{ParentEnv: "opencode:B", "CLAUDE_CODE_SESSION_ID": "A"}
	claudeHook(t, tr, w, 301, "C", agent.Start)
	c := get(t, tr, "claude:C")
	if c.ParentID != "opencode:B" || c.Kind != store.KindHeadless {
		t.Fatalf("C = %+v, want child of opencode:B, headless", c)
	}
}

func TestDetachedChildrenLinkThroughEnvironment(t *testing.T) {
	tr, w := newWorld(t)
	claudeHook(t, tr, w, 100, "A", agent.Start)

	// nohup/setsid: the process was reparented to launchd.
	w.add(401, 1, "opencode run background job")
	w.env = map[string]string{ParentEnv: "claude:A"}
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "D", Type: agent.Start, PID: 401})
	if d := get(t, tr, "opencode:D"); d.ParentID != "claude:A" || d.Kind != store.KindHeadless {
		t.Fatalf("D = %+v, want child of claude:A via HIVE_PARENT, headless", d)
	}

	// Without HIVE_PARENT, the adapter's own variable is the fallback.
	w.add(501, 1, "claude -p detached")
	w.env = map[string]string{"CLAUDE_CODE_SESSION_ID": "A"}
	claudeHook(t, tr, w, 501, "E", agent.Start)
	if e := get(t, tr, "claude:E"); e.ParentID != "claude:A" {
		t.Fatalf("E parent = %q, want claude:A via CLAUDE_CODE_SESSION_ID", e.ParentID)
	}

	// A variable naming the session itself is not a parent.
	w.add(601, 1, "claude")
	w.env = map[string]string{"CLAUDE_CODE_SESSION_ID": "F"}
	claudeHook(t, tr, w, 601, "F", agent.Start)
	if f := get(t, tr, "claude:F"); f.ParentID != "" {
		t.Fatalf("F parent = %q, want none", f.ParentID)
	}
}

func TestUntrackedParentIsAdoptedFromTheEnvironment(t *testing.T) {
	tr, w := newWorld(t)
	// Claude A (pid 100) started before hive was installed, so it never
	// reported in. Its shell tool still exports its session ID.
	w.env = map[string]string{"CLAUDE_CODE_SESSION_ID": "A"}
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "B", Type: agent.Start, PID: 201})

	if b := get(t, tr, "opencode:B"); b.ParentID != "claude:A" {
		t.Fatalf("B parent = %q, want claude:A", b.ParentID)
	}
	a := get(t, tr, "claude:A")
	if a.PID != 100 || a.Pane != "%1" || a.Source != "inferred" || a.Status != store.StatusUnknown || !a.Live() {
		t.Fatalf("A = %+v, want adopted as live pid 100 in %%1, status unknown", a)
	}
	sessions, err := tr.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if s.ID == "claude:pid-100" {
			t.Fatal("adopted process still reported as untracked")
		}
	}
}

func TestNewSessionInSameProcessEndsThePreviousOne(t *testing.T) {
	tr, w := newWorld(t)
	claudeHook(t, tr, w, 100, "A", agent.Start)
	w.now = 2_000
	claudeHook(t, tr, w, 100, "A2", agent.Start) // /clear

	if a := get(t, tr, "claude:A"); a.Status != store.StatusExited || a.PID != 0 {
		t.Fatalf("A = %+v, want exited", a)
	}
	if a2 := get(t, tr, "claude:A2"); a2.PID != 100 || a2.Pane != "%1" || a2.ParentID != "" {
		t.Fatalf("A2 = %+v, want pid 100 in %%1, no parent", a2)
	}
}

func TestOutOfOrderEventsKeepTheNewestStatus(t *testing.T) {
	tr, _ := newWorld(t)
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "B", Type: agent.Busy, PID: 201, At: 2_000})
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "B", Type: agent.Start, PID: 201, At: 1_000})
	if b := get(t, tr, "opencode:B"); b.Status != store.StatusWorking || b.CreatedAt != 1_000 {
		t.Fatalf("B = %+v, want working, created at 1000", b)
	}
}

func TestInternalSubagents(t *testing.T) {
	tr, w := newWorld(t)
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "B", Type: agent.Start, PID: 201})
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "G", Type: agent.Start, PID: 201,
		ParentID: "opencode:B", Internal: true})
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "G", Type: agent.Busy, PID: 201})

	g := get(t, tr, "opencode:G")
	if g.ParentID != "opencode:B" || g.Kind != store.KindInternal || g.PID != 0 || !g.Live() {
		t.Fatalf("G = %+v, want live internal child of opencode:B without a pid", g)
	}
	if b := get(t, tr, "opencode:B"); b.PID != 201 || b.Status == store.StatusExited {
		t.Fatalf("B = %+v, a subagent must not end its parent", b)
	}

	// When B's process goes away, B and its subagent both end.
	delete(w.procs, 201)
	w.now = 5_000
	if _, err := tr.Refresh(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"opencode:B", "opencode:G"} {
		if s := get(t, tr, id); s.Status != store.StatusExited || s.Live() {
			t.Fatalf("%s = %+v, want exited", id, s)
		}
	}
}

func TestLaunchRecordNamesTheParent(t *testing.T) {
	tr, w := newWorld(t)
	claudeHook(t, tr, w, 100, "A", agent.Start)
	if err := tr.Store.PutLaunch(store.Launch{Pane: "%2", Tool: "opencode", ParentID: "claude:A", CreatedAt: w.now}); err != nil {
		t.Fatal(err)
	}
	// hive started opencode in pane %2 with `tmux new-window`, so the process
	// tree runs through the tmux server, not through A.
	w.add(96, 95, "opencode")
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "H", Type: agent.Start, PID: 96})
	if h := get(t, tr, "opencode:H"); h.ParentID != "claude:A" || h.Pane != "%2" || h.Kind != store.KindInteractive {
		t.Fatalf("H = %+v, want interactive child of claude:A in %%2", h)
	}
}

func TestEndOfUnknownSessionIsIgnored(t *testing.T) {
	tr, _ := newWorld(t)
	ingest(t, tr, agent.Event{Tool: "claude", SessionID: "A/helper", Type: agent.End, Internal: true, ParentID: "claude:A"})
	if _, ok, _ := tr.Store.Get("claude:A/helper"); ok {
		t.Fatal("an end event created a session")
	}
}

func TestParentCyclesAreRejected(t *testing.T) {
	tr, _ := newWorld(t)
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "X", Type: agent.Update, ParentID: "opencode:Y", Internal: true})
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "Y", Type: agent.Update, ParentID: "opencode:X", Internal: true})
	if y := get(t, tr, "opencode:Y"); y.ParentID != "" {
		t.Fatalf("Y parent = %q, want none: X already descends from Y", y.ParentID)
	}
}

func TestRefreshFindsUntrackedAgents(t *testing.T) {
	tr, w := newWorld(t)
	claudeHook(t, tr, w, 100, "A", agent.Start)
	w.add(96, 95, "opencode")        // started before hive was installed
	w.add(97, 96, "opencode worker") // its own worker process, not a session

	sessions, err := tr.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]store.Session{}
	for _, s := range sessions {
		got[s.ID] = s
	}
	u, ok := got["opencode:pid-96"]
	if !ok || u.Pane != "%2" || u.Source != "scan" || u.Kind != store.KindInteractive {
		t.Fatalf("untracked opencode = %+v (found %v), want pane %%2, interactive", u, ok)
	}
	if _, ok := got["opencode:pid-97"]; ok {
		t.Fatal("worker process reported as a separate session")
	}
	// Tracked processes and the processes under them are not duplicated...
	if _, ok := got["claude:pid-100"]; ok {
		t.Fatal("tracked claude reported as untracked")
	}
	// ...but agents running inside a tracked agent are found, with their parent.
	if b := got["opencode:pid-201"]; b.ParentID != "claude:A" || b.Kind != store.KindHeadless {
		t.Fatalf("untracked opencode run = %+v, want headless child of claude:A", b)
	}
}

func TestRefreshClaimsOrphansFromHistory(t *testing.T) {
	tr, w := newWorld(t)
	// Claude A (pid 100, cwd /work) started at 500, before hive was installed;
	// its transcript was imported and it has been active since.
	p := w.procs[100]
	p.Started = 500
	w.procs[100] = p
	for _, s := range []store.Session{
		{ID: "claude:A", Tool: "claude", NativeID: "A", Cwd: "/work", CreatedAt: 400, UpdatedAt: 900},
		{ID: "claude:old", Tool: "claude", NativeID: "old", Cwd: "/work", CreatedAt: 100, UpdatedAt: 200},
		{ID: "claude:elsewhere", Tool: "claude", NativeID: "elsewhere", Cwd: "/other", CreatedAt: 600, UpdatedAt: 950},
	} {
		s.Status = store.StatusExited
		if err := tr.Store.Upsert(s); err != nil {
			t.Fatal(err)
		}
	}
	sessions, err := tr.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	a := get(t, tr, "claude:A")
	if a.PID != 100 || a.Pane != "%1" || !a.Live() {
		t.Fatalf("A = %+v, want claimed by pid 100 in %%1", a)
	}
	for _, s := range sessions {
		if s.ID == "claude:pid-100" {
			t.Fatal("claimed process still reported as untracked")
		}
	}

	// Two sessions active since the process started: ambiguous, no claim.
	tr2, w2 := newWorld(t)
	p = w2.procs[100]
	p.Started = 500
	w2.procs[100] = p
	for _, id := range []string{"x", "y"} {
		tr2.Store.Upsert(store.Session{ID: "claude:" + id, Tool: "claude", NativeID: id, Cwd: "/work", CreatedAt: 600, UpdatedAt: 900})
	}
	if _, err := tr2.Refresh(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"claude:x", "claude:y"} {
		if s := get(t, tr2, id); s.PID != 0 {
			t.Errorf("%s claimed despite ambiguity: %+v", id, s)
		}
	}
}

func TestLaunchedAgentShowsUnderItsParentBeforeReportingIn(t *testing.T) {
	tr, w := newWorld(t)
	claudeHook(t, tr, w, 100, "A", agent.Start)
	tr.Store.PutLaunch(store.Launch{Pane: "%2", Tool: "opencode", ParentID: "claude:A", CreatedAt: w.now})
	w.add(96, 95, "/opt/homebrew/bin/opencode") // the TUI, before its first message
	sessions, err := tr.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if s.PID == 96 {
			if s.ParentID != "claude:A" || s.Source != "pending" || s.Pane != "%2" {
				t.Fatalf("pending launch = %+v", s)
			}
			return
		}
	}
	t.Fatal("launched agent not listed")
}

func codexHook(t *testing.T, tr *Tracker, w *fakeWorld, id string, typ agent.EventType, cwd string) {
	t.Helper()
	w.add(800, 1, "/Users/me/.codex/bin/codex app-server --listen unix:// --managed-daemon")
	w.add(9800, 800, "/bin/sh -c hive hook codex")
	w.add(9850, 9800, "hive hook codex")
	w.self = 9850
	ingest(t, tr, agent.Event{Tool: "codex", SessionID: id, Type: typ, Cwd: cwd})
}

func TestDaemonHostedSessionFindsItsProcessInTheLaunchedPane(t *testing.T) {
	tr, w := newWorld(t)
	claudeHook(t, tr, w, 100, "A", agent.Start)
	w.add(700, 80, "/opt/homebrew/bin/codex")
	w.panes = append(w.panes, tmux.Pane{ID: "%3", PID: 700})
	if err := tr.Store.PutLaunch(store.Launch{Pane: "%3", Tool: "codex", ParentID: "claude:A", Cwd: "/work", CreatedAt: w.now}); err != nil {
		t.Fatal(err)
	}
	codexHook(t, tr, w, "X", agent.Start, "/work")
	x := get(t, tr, "codex:X")
	if x.PID != 700 || x.Pane != "%3" || x.ParentID != "claude:A" || x.Kind != store.KindInteractive || x.Status != store.StatusIdle {
		t.Fatalf("X = %+v, want pid 700 in %%3, child of claude:A, idle", x)
	}
	if a := get(t, tr, "claude:A"); !a.Live() {
		t.Fatalf("A = %+v, the daemon's pid must not be treated as a session process", a)
	}
}

func TestDaemonHostedSessionFindsTheOnlyProcessInItsFolder(t *testing.T) {
	tr, w := newWorld(t)
	w.add(700, 95, "codex")
	codexHook(t, tr, w, "X", agent.Start, "/work")
	if x := get(t, tr, "codex:X"); x.PID != 700 || x.Pane != "%2" || x.ParentID != "" {
		t.Fatalf("X = %+v, want pid 700 in %%2 with no parent", x)
	}

	tr2, w2 := newWorld(t)
	w2.add(700, 95, "codex")
	w2.add(701, 95, "codex")
	codexHook(t, tr2, w2, "Y", agent.Start, "/work")
	if y := get(t, tr2, "codex:Y"); y.PID != 0 {
		t.Fatalf("Y = %+v, want no process while two unclaimed TUIs run in /work", y)
	}
}

func TestRefreshClaimsDaemonHostedSessionAndAdoptsItsLaunch(t *testing.T) {
	tr, w := newWorld(t)
	claudeHook(t, tr, w, 100, "A", agent.Start)
	w.add(700, 95, "codex")
	p := w.procs[700]
	p.Started = 500
	w.procs[700] = p
	tr.Store.PutLaunch(store.Launch{Pane: "%2", Tool: "codex", ParentID: "claude:A", Cwd: "/work", CreatedAt: w.now})
	for _, s := range []store.Session{
		{ID: "codex:X", Tool: "codex", NativeID: "X", Cwd: "/work", Status: store.StatusIdle, StatusAt: 900, CreatedAt: 900, UpdatedAt: 900, Source: "hook"},
		{ID: "codex:Y", Tool: "codex", NativeID: "Y", Cwd: "/work", Status: store.StatusExited, StatusAt: 950, CreatedAt: 300, UpdatedAt: 950, Source: "hook"},
	} {
		if err := tr.Store.Upsert(s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tr.Refresh(); err != nil {
		t.Fatal(err)
	}
	x := get(t, tr, "codex:X")
	if x.PID != 700 || x.Pane != "%2" || x.ParentID != "claude:A" || x.Status != store.StatusIdle {
		t.Fatalf("X = %+v, want claimed by pid 700 in %%2 under claude:A, still idle", x)
	}
	if y := get(t, tr, "codex:Y"); y.PID != 0 {
		t.Fatalf("Y = %+v, want left alone", y)
	}
	if launches, _ := tr.Store.Launches(); len(launches) != 0 {
		t.Fatalf("launch record kept after the claim: %+v", launches)
	}
}

func TestClaudeStartExportsParentForItsShell(t *testing.T) {
	tr, w := newWorld(t)
	envFile := filepath.Join(t.TempDir(), "env")
	w.add(9100, 100, "/bin/sh -c hive hook claude")
	w.add(9600, 9100, "hive hook claude")
	w.self = 9600
	ingest(t, tr, agent.Event{Tool: "claude", SessionID: "A", Type: agent.Start, EnvFile: envFile})
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := "export HIVE_PARENT=claude:A\n"; string(data) != want {
		t.Fatalf("env file = %q, want %q", data, want)
	}
}
