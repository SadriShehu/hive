package tracker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

// leadUp makes Claude L (pid 105, pane %2) report in as the lead.
func leadUp(t *testing.T, tr *Tracker, w *fakeWorld) {
	t.Helper()
	w.add(105, 95, "claude")
	if err := tr.Store.PutLaunchTeam(store.LaunchTeam{Pane: "%2", TeamID: "t1", Role: "lead", CreatedAt: w.now}); err != nil {
		t.Fatal(err)
	}
	claudeHook(t, tr, w, 105, "L", agent.Start)
}

// addTask puts a task on the api team's board as the lead.
func addTask(t *testing.T, tr *Tracker, title, owner string, after ...int) store.Task {
	t.Helper()
	task, _, err := tr.AddTask(theTeam(t, tr), "lead", title, "", owner, append([]int{}, after...))
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// typedInto returns what was typed into pane, one paste each.
func typedInto(sent []typed, pane string) []string {
	var out []string
	for _, s := range sent {
		if s.pane == pane {
			out = append(out, s.text)
		}
	}
	return out
}

func TestAReadyTaskIsToldToItsOwnerAsItIsAdded(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)

	addTask(t, tr, "schema", "backend")
	addTask(t, tr, "handlers", "backend", 1) // waits for #1: nothing to tell yet
	addTask(t, tr, "docs", "")
	got := typedInto(*sent, "%1")
	want := "[api · from hive] #1 schema is yours, and ready. `hive team task claim 1` takes it."
	if len(*sent) != 1 || len(got) != 1 || got[0] != want {
		t.Fatalf("typed %+v, want %q into backend's pane only", *sent, want)
	}
	// hive's word to the owner stays out of the user's feed; the board shows it.
	if msgs, _ := tr.Store.Messages("t1", 50, false); len(msgs) != 0 {
		t.Errorf("the feed shows %+v", msgs)
	}

	if _, _, err := tr.AddTask(theTeam(t, tr), "lead", "x", "", "nobody", nil); err == nil || !strings.Contains(err.Error(), "roles:") {
		t.Errorf("an owner off the team = %v, want the roles named", err)
	}
	if _, _, err := tr.AddTask(theTeam(t, tr), "lead", "x", "", "", []int{7}); err == nil || !strings.Contains(err.Error(), "#7") {
		t.Errorf("waiting for a task that isn't there = %v", err)
	}
}

func TestEditTaskChangesItsFieldsAndChecksTheBoard(t *testing.T) {
	tr, _, _ := newTeamWorld(t)
	addTask(t, tr, "schema", "backend")
	addTask(t, tr, "handlers", "", 1)
	tm := theTeam(t, tr)

	task, err := tr.EditTask(tm, 2, "handlers v2", "the admin endpoints", "frontend", []int{1})
	if err != nil {
		t.Fatal(err)
	}
	if task.Title != "handlers v2" || task.Body != "the admin endpoints" || task.Owner != "frontend" {
		t.Fatalf("EditTask = %+v", task)
	}
	got, _ := tr.Store.Task("t1", 2)
	if got.Title != "handlers v2" || got.Body != "the admin endpoints" || got.Owner != "frontend" ||
		len(got.After) != 1 || got.After[0] != 1 || got.Status != store.TaskOpen {
		t.Errorf("#2 = %+v, want the edit kept", got)
	}

	if _, err := tr.EditTask(tm, 2, "x", "", "nobody", nil); err == nil || !strings.Contains(err.Error(), "roles:") {
		t.Errorf("an owner off the team = %v, want the roles named", err)
	}
	if _, err := tr.EditTask(tm, 2, "x", "", "", []int{7}); err == nil || !strings.Contains(err.Error(), "#7") {
		t.Errorf("waiting for a task that isn't there = %v", err)
	}
	if _, err := tr.EditTask(tm, 2, "x", "", "", []int{2}); err == nil || !strings.Contains(err.Error(), "itself") {
		t.Errorf("waiting for itself = %v, want it refused", err)
	}
	if _, err := tr.EditTask(tm, 1, "schema", "", "backend", []int{2}); err == nil || !strings.Contains(err.Error(), "waits for it") {
		t.Errorf("a dependency cycle = %v, want it refused", err)
	}
	if _, err := tr.EditTask(tm, 2, " ", "", "", nil); err == nil {
		t.Error("an empty title was kept")
	}
}

func TestDoneTellsTheLeadAndTheOwnersOfReadyTasks(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)
	leadUp(t, tr, w)
	addTask(t, tr, "schema", "backend")
	addTask(t, tr, "table view", "frontend", 1)
	addTask(t, tr, "tests", "backend", 1, 2)
	*sent = nil

	tm := theTeam(t, tr)
	if _, err := tr.ClaimTask(tm, "backend", 1, false); err != nil {
		t.Fatal(err)
	}
	_, ready, _, err := tr.DoneTask(tm, "backend", 1, "users.role added")
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 || ready[0].N != 2 {
		t.Fatalf("ready = %+v, want #2 only: #3 still waits for #2", ready)
	}
	// The lead, idle in %2, reads it at once; backend, who closed it, gets nothing.
	want := "[api · from hive] #1 done by backend: users.role added · ready: #2 (frontend)"
	if got := typedInto(*sent, "%2"); len(got) != 1 || got[0] != want {
		t.Fatalf("typed into the lead %q, want %q", got, want)
	}
	if got := typedInto(*sent, "%1"); len(got) != 0 {
		t.Errorf("typed into backend %q", got)
	}
	// frontend hasn't reported in: its note waits for it.
	pending, _ := tr.Store.Pending("t1", "frontend")
	if len(pending) != 1 || !strings.HasPrefix(pending[0].Body, "#2 table view is yours, and ready (#1 done by backend: users.role added)") {
		t.Fatalf("queued for frontend = %+v", pending)
	}
	if got := hivePosts(tr); !strings.Contains(got, "#1 claimed by backend: schema|"+strings.TrimPrefix(want, "[api · from hive] ")) {
		t.Errorf("hive posts = %q, want the claim and the close", got)
	}

	// frontend does #2: #3 is ready, and backend is told.
	*sent = nil
	tr.ClaimTask(tm, "frontend", 2, false)
	if _, _, _, err := tr.DoneTask(tm, "frontend", 2, ""); err != nil {
		t.Fatal(err)
	}
	if got := typedInto(*sent, "%1"); len(got) != 1 || !strings.HasPrefix(got[0], "[api · from hive] #3 tests is yours, and ready (#2 done by frontend: table view)") {
		t.Errorf("typed into backend %q, want #3 ready", got)
	}
	if _, _, _, err := tr.DoneTask(tm, "frontend", 2, ""); err == nil {
		t.Error("a task was done twice")
	}
}

func TestClaimRules(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)
	addTask(t, tr, "schema", "backend")
	addTask(t, tr, "handlers", "", 1)
	*sent = nil
	tm := theTeam(t, tr)

	if _, err := tr.ClaimTask(tm, "frontend", 2, false); err == nil || !strings.Contains(err.Error(), "waits for #1") {
		t.Errorf("claiming #2 before #1 is done = %v, want #1 named", err)
	}
	if _, err := tr.ClaimTask(tm, "frontend", 1, false); err == nil || !strings.Contains(err.Error(), "backend's") {
		t.Errorf("claiming backend's task = %v", err)
	}
	if _, err := tr.ClaimTask(tm, store.FromYou, 1, false); err == nil || !strings.Contains(err.Error(), "--as") {
		t.Errorf("claiming as the user = %v, want it refused", err)
	}
	if _, err := tr.ClaimTask(tm, "frontend", 9, false); err == nil {
		t.Error("claimed a task that isn't there")
	}

	tr.SetPaused(tm, true)
	if _, err := tr.ClaimTask(tm, "backend", 1, false); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Errorf("claiming on a paused team = %v", err)
	}
	if _, err := tr.ClaimTask(tm, "frontend", 2, true); err == nil {
		t.Error("--force claimed on a paused team")
	}
	tr.SetPaused(tm, false)

	if _, err := tr.ClaimTask(tm, "backend", 1, false); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.ClaimTask(tm, "backend", 1, false); err != nil {
		t.Errorf("claiming one's own task again = %v", err)
	}
	if n := strings.Count(hivePosts(tr), "#1 claimed"); n != 1 {
		t.Errorf("hive posted the claim %d times", n)
	}
	if task, err := tr.ClaimTask(tm, "frontend", 2, true); err != nil || task.Owner != "frontend" {
		t.Errorf("--force = %+v, %v", task, err)
	}

	// Taking a task over with --force: the member that held it is told.
	*sent = nil
	if _, err := tr.ClaimTask(tm, "frontend", 1, true); err != nil {
		t.Fatal(err)
	}
	if got := typedInto(*sent, "%1"); len(got) != 1 || got[0] != "[api · from hive] #1 claimed by frontend: schema" {
		t.Errorf("typed into backend %q", got)
	}
}

func TestBlockedGoesToTheLeadOrTheChannel(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)
	leadUp(t, tr, w)
	addTask(t, tr, "schema", "backend")
	*sent = nil
	tm := theTeam(t, tr)

	if _, _, err := tr.BlockTask(tm, "backend", 1, " "); err == nil {
		t.Error("blocked without saying why")
	}
	if _, _, err := tr.BlockTask(tm, "backend", 1, "no database access"); err != nil {
		t.Fatal(err)
	}
	if got := typedInto(*sent, "%2"); len(got) != 1 || got[0] != "[api · from hive] #1 blocked by backend: no database access" {
		t.Fatalf("typed into the lead %q", got)
	}

	// When the user leads, the news is a post for the channel, typed nowhere.
	*sent = nil
	tr.SetLead(tm, "")
	tm = theTeam(t, tr)
	tr.ClaimTask(tm, "backend", 1, false)
	if _, _, _, err := tr.DoneTask(tm, "backend", 1, "granted"); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 0 {
		t.Errorf("typed %+v with the user leading", *sent)
	}
	msgs, _ := tr.Store.Messages("t1", 1, false)
	if len(msgs) != 1 || msgs[0].Body != "#1 done by backend: granted" || msgs[0].To != "" {
		t.Errorf("the last post = %+v", msgs)
	}
	if pending, _ := tr.Store.HasPending("t1", "lead"); pending {
		t.Error("the user leads, but the agent called lead was sent the news")
	}
}

// waitFor runs WaitTeam with a quick poll, making one of changes before each
// look again, and returns its lines and how many times it looked again. It
// gives up after timeout.
func waitFor(t *testing.T, tr *Tracker, w *fakeWorld, role string, o WaitOptions, timeout time.Duration, changes ...func()) ([]string, int, error) {
	t.Helper()
	looks := 0
	w.onForget = func() {
		i := looks
		looks++
		if i < len(changes) {
			changes[i]()
		}
	}
	defer func() { w.onForget = nil }()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	o.Poll = time.Millisecond
	lines, err := tr.WaitTeam(ctx, theTeam(t, tr), role, o)
	return lines, looks, err
}

func TestWaitForATaskGoesOnUntilItCloses(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	addTask(t, tr, "schema", "backend")
	addTask(t, tr, "docs", "frontend")
	tm := theTeam(t, tr)

	lines, looks, err := waitFor(t, tr, w, "lead", WaitOptions{Task: 1}, time.Second,
		func() { tr.ClaimTask(tm, "backend", 1, false) },
		func() { tr.DoneTask(tm, "frontend", 2, "") }, // not the one waited for
		func() { tr.DoneTask(tm, "backend", 1, "schema in") },
	)
	if err != nil || strings.Join(lines, "|") != "#1 done by backend: schema in" || looks != 3 {
		t.Fatalf("WaitTeam = %q, %v after %d looks; want #1's close after the third", lines, err, looks)
	}

	// A task closed already returns at once.
	lines, looks, err = waitFor(t, tr, w, "lead", WaitOptions{Task: 2}, time.Second)
	if err != nil || strings.Join(lines, "|") != "#2 done by frontend: docs" || looks != 0 {
		t.Errorf("WaitTeam on a done task = %q, %v after %d looks", lines, err, looks)
	}

	if _, _, err := waitFor(t, tr, w, "lead", WaitOptions{Task: 9}, time.Second); err == nil {
		t.Error("waited for a task that isn't there")
	}
	if _, _, err := waitFor(t, tr, w, "lead", WaitOptions{From: "nobody"}, time.Second); err == nil {
		t.Error("waited for a role that isn't on the team")
	}
}

func TestWaitFromARoleTakesItsMessage(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)
	claudeHook(t, tr, w, 100, "A", agent.Prompt) // backend works: mail to it waits
	tm := theTeam(t, tr)

	lines, _, err := waitFor(t, tr, w, "backend", WaitOptions{From: "lead"}, time.Second,
		func() { tr.Say(tm, "frontend", "backend", "not this one", false) },
		func() { tr.Say(tm, "lead", "backend", "take #4 next", false) },
	)
	if err != nil || strings.Join(lines, "|") != "[api · from lead] take #4 next" {
		t.Fatalf("WaitTeam = %q, %v", lines, err)
	}
	// What it printed isn't typed again at the next idle; the rest is.
	pending, _ := tr.Store.Pending("t1", "backend")
	if len(pending) != 1 || pending[0].From != "frontend" {
		t.Errorf("still queued = %+v, want frontend's only", pending)
	}

	// Mail that was waiting before the wait began returns at once.
	tr.Say(tm, "lead", "backend", "and #5", false)
	lines, looks, err := waitFor(t, tr, w, "backend", WaitOptions{From: "lead"}, time.Second)
	if err != nil || strings.Join(lines, "|") != "[api · from lead] and #5" || looks != 0 {
		t.Errorf("WaitTeam = %q, %v after %d looks", lines, err, looks)
	}
	if len(*sent) != 0 {
		t.Errorf("typed %+v into a working member", *sent)
	}
}

func TestWaitWithNoFlagReturnsOnACloseOrMail(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	addTask(t, tr, "schema", "backend")
	tm := theTeam(t, tr)

	// Adding or claiming isn't closing.
	lines, looks, err := waitFor(t, tr, w, "lead", WaitOptions{}, time.Second,
		func() { addTask(t, tr, "docs", "") },
		func() { tr.ClaimTask(tm, "backend", 1, false) },
		func() { tr.DoneTask(tm, "backend", 1, "schema in") },
	)
	// The lead's mail about #1 says what the task's own line would, and more.
	if err != nil || strings.Join(lines, "|") != "[api · from hive] #1 done by backend: schema in" || looks != 3 {
		t.Fatalf("WaitTeam = %q, %v after %d looks", lines, err, looks)
	}
	if pending, _ := tr.Store.HasPending("t1", "lead"); pending {
		t.Error("the lead's mail is still queued after the wait printed it")
	}
}

func TestWaitAnyReturnsOnNewTasksAndTheChannel(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	tm := theTeam(t, tr)

	lines, _, err := waitFor(t, tr, w, "frontend", WaitOptions{Any: true}, time.Second,
		func() { addTask(t, tr, "docs", "") })
	if err != nil || strings.Join(lines, "|") != "#1 added by lead: docs" {
		t.Fatalf("WaitTeam = %q, %v", lines, err)
	}
	lines, _, err = waitFor(t, tr, w, "frontend", WaitOptions{Any: true}, time.Second,
		func() { tr.Post(tm, "backend", "schema is in") })
	if err != nil || strings.Join(lines, "|") != "[api · from backend] schema is in" {
		t.Fatalf("WaitTeam = %q, %v", lines, err)
	}

	// A caller off the team reads the channel: here, the user from a shell.
	lines, _, err = waitFor(t, tr, w, "", WaitOptions{From: "backend"}, time.Second,
		func() { tr.Say(tm, "lead", "backend", "thanks", false) },
		func() { tr.Say(tm, "backend", "lead", "next?", false) })
	if err != nil || strings.Join(lines, "|") != "[api · from backend → lead] next?" {
		t.Fatalf("WaitTeam = %q, %v", lines, err)
	}
}

func TestWaitTimesOutAndLeavesAPausedTeamsMail(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)
	tr.SetPaused(tm, true)
	tr.Say(theTeam(t, tr), "lead", "backend", "hold on", false)

	_, _, err := waitFor(t, tr, w, "backend", WaitOptions{From: "lead"}, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitTeam = %v, want the deadline", err)
	}
	if pending, _ := tr.Store.HasPending("t1", "backend"); !pending {
		t.Error("the wait took a paused team's mail")
	}
}
