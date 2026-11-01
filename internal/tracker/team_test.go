package tracker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
	"github.com/sadrishehu/hive/internal/tmux"
)

// typed records what a tracker typed into panes.
type typed struct {
	pane, text string
}

// newTeamWorld is newWorld with a team "api" whose backend is to run in pane
// %1 (Claude A, pid 100) and whose lead sits in pane %2 (not started yet).
func newTeamWorld(t *testing.T) (*Tracker, *fakeWorld, *[]typed) {
	t.Helper()
	tr, w := newWorld(t)
	var sent []typed
	tr.Type = func(pane, text string) error { sent = append(sent, typed{pane, text}); return nil }
	tr.HasPane = func(pane string) bool { return pane == "%1" || pane == "%2" }

	tm := store.Team{ID: "t1", Name: "api", Goal: "ship it", Cwd: "/work", Workspace: store.WorkspaceShared,
		Status: store.TeamActive, LeadRole: "lead", CreatedAt: 1, UpdatedAt: 1}
	if err := tr.Store.CreateTeam(tm); err != nil {
		t.Fatal(err)
	}
	for _, m := range []store.Member{
		{TeamID: "t1", Role: "lead", Tool: "claude", JoinedAt: 1},
		{TeamID: "t1", Role: "backend", Tool: "claude", JoinedAt: 2},
		{TeamID: "t1", Role: "frontend", Tool: "claude", JoinedAt: 3},
	} {
		if err := tr.Store.AddMember(m); err != nil {
			t.Fatal(err)
		}
	}
	return tr, w, &sent
}

func theTeam(t *testing.T, tr *Tracker) store.Team {
	t.Helper()
	tm, err := tr.Store.Team("api")
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

// backendUp makes Claude A (pid 100, pane %1) report in as the backend.
func backendUp(t *testing.T, tr *Tracker, w *fakeWorld) {
	t.Helper()
	if err := tr.Store.PutLaunchTeam(store.LaunchTeam{Pane: "%1", TeamID: "t1", Role: "backend", CreatedAt: w.now}); err != nil {
		t.Fatal(err)
	}
	claudeHook(t, tr, w, 100, "A", agent.Start)
}

// roleOf returns the one role session id plays, or "".
func roleOf(t *testing.T, tr *Tracker, id string) string {
	t.Helper()
	ms, err := tr.Store.Memberships(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		return ""
	}
	return ms[0].Role
}

// hivePosts returns what hive itself wrote to the api team's channel.
func hivePosts(tr *Tracker) string {
	msgs, _ := tr.Store.Messages("t1", 50, false)
	var bodies []string
	for _, m := range msgs {
		if m.Kind == store.MsgSystem && m.From == store.FromHive {
			bodies = append(bodies, m.Body)
		}
	}
	return strings.Join(bodies, "|")
}

func TestMemberIsLinkedFromItsLaunchRecord(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	backendUp(t, tr, w)

	ms, err := tr.Store.Memberships("claude:A")
	if err != nil || len(ms) != 1 || ms[0].Role != "backend" || ms[0].Pane != "%1" {
		t.Fatalf("Memberships = %+v %v, want backend in %%1", ms, err)
	}
	if left, _ := tr.Store.TakeLaunchTeams("%1", 0); len(left) != 0 {
		t.Fatal("the launch record is kept after the agent reported in")
	}
}

func TestMemberResumedUnderANewSessionKeepsItsRole(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	backendUp(t, tr, w)

	// The agent is reopened in a new window, and continues under another ID
	// in another process, as Codex and opencode do.
	claudeHook(t, tr, w, 100, "A", agent.End)
	if err := tr.rememberMember("claude:A", tmux.Pane{ID: "%2", PID: 95}); err != nil {
		t.Fatal(err)
	}
	w.add(96, 95, "claude")
	claudeHook(t, tr, w, 96, "A2", agent.Start)

	if got := roleOf(t, tr, "claude:A2"); got != "backend" {
		t.Fatalf("A2 plays %q, want backend", got)
	}
	if got := roleOf(t, tr, "claude:A"); got != "" {
		t.Fatalf("the old session still plays %s", got)
	}
}

func TestSayToAnIdleMemberTypesAtOnce(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)

	_, got, err := tr.Say(tm, "lead", "backend", "the list endpoint is GET /admin/users", false)
	if err != nil {
		t.Fatal(err)
	}
	want := "[api · from lead] the list endpoint is GET /admin/users"
	if len(*sent) != 1 || (*sent)[0].pane != "%1" || (*sent)[0].text != want {
		t.Fatalf("typed %+v, want %q into %%1", *sent, want)
	}
	if len(got) != 1 || got[0].Role != "backend" || got[0].How != "sent" {
		t.Fatalf("result = %+v", got)
	}
	if pending, _ := tr.Store.HasPending("t1", "backend"); pending {
		t.Fatal("the message is still queued after it was typed")
	}
	// hive wait on the member waits for the answer.
	if _, ok, _ := tr.Store.Input("claude:A"); !ok {
		t.Fatal("no input recorded for the member")
	}
}

func TestSayToAWorkingMemberWaitsForItsIdleEvent(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)
	var woken []string
	tr.OnDeliverable = func(s store.Session) { woken = append(woken, s.ID) }
	tm := theTeam(t, tr)

	claudeHook(t, tr, w, 100, "A", agent.Prompt) // working
	tr.Say(tm, "lead", "backend", "one", false)
	tr.Say(tm, "frontend", "backend", "two", false)
	if len(*sent) != 0 {
		t.Fatalf("typed %+v into a working member", *sent)
	}
	if pending, _ := tr.Store.HasPending("t1", "backend"); !pending {
		t.Fatal("nothing queued for the working member")
	}

	claudeHook(t, tr, w, 100, "A", agent.Idle)
	if len(woken) != 1 || woken[0] != "claude:A" {
		t.Fatalf("OnDeliverable = %v, want [claude:A]", woken)
	}
	how, err := tr.Deliver("claude:A", 0)
	if err != nil || how != "sent" {
		t.Fatalf("Deliver = %q, %v", how, err)
	}
	// Both messages arrive as one paste, one line each.
	want := "[api · from lead] one\n[api · from frontend] two"
	if len(*sent) != 1 || (*sent)[0].text != want {
		t.Fatalf("typed %+v, want one paste %q", *sent, want)
	}
}

func TestMailDoesNotAnswerAPermissionPrompt(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)

	claudeHook(t, tr, w, 100, "A", agent.Attention)
	_, got, err := tr.Say(tm, "lead", "backend", "hello", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 0 || !strings.Contains(got[0].How, "waiting on you") {
		t.Fatalf("typed %+v, result %+v; want it queued behind the prompt", *sent, got)
	}
}

func TestPausedTeamQueuesUntilResumed(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)

	if err := tr.SetPaused(tm, true); err != nil {
		t.Fatal(err)
	}
	tm = theTeam(t, tr)
	_, got, _ := tr.Say(tm, "lead", "backend", "hold on", false)
	if len(*sent) != 0 || !strings.Contains(got[0].How, "paused") {
		t.Fatalf("typed %+v, result %+v; want it queued", *sent, got)
	}
	woken := false
	tr.OnDeliverable = func(store.Session) { woken = true }
	claudeHook(t, tr, w, 100, "A", agent.Idle)
	if woken {
		t.Fatal("a paused team asked for a delivery")
	}

	if err := tr.SetPaused(tm, false); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 1 || !strings.HasSuffix((*sent)[0].text, "hold on") {
		t.Fatalf("typed %+v after resuming, want the queued message", *sent)
	}
}

func TestPostReachesNoPaneUnlessItNamesSomeone(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)

	if _, _, err := tr.Post(tm, "lead", "plan is up"); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 0 {
		t.Fatalf("a plain post typed %+v", *sent)
	}
	if _, got, err := tr.Post(tm, "lead", "@backend can you take the schema?"); err != nil || len(got) != 1 {
		t.Fatalf("Post = %+v, %v; want one delivery to backend", got, err)
	}
	if len(*sent) != 1 || (*sent)[0].pane != "%1" {
		t.Fatalf("typed %+v, want one message in %%1", *sent)
	}
	msgs, _ := tr.Store.Messages("t1", 10, false)
	if len(msgs) != 2 || msgs[0].Kind != store.MsgPost {
		t.Fatalf("channel = %+v, want both posts", msgs)
	}
}

func TestPostKeepsControlCharactersOutOfTheChannel(t *testing.T) {
	tr, _, _ := newTeamWorld(t)
	tm := theTeam(t, tr)
	msg, _, err := tr.Post(tm, "lead", "before\x1b]52;c;bm90IHNhZmU=\x07after")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(msg.Body, "\x1b\x07\x00\x9b") {
		t.Fatalf("stored post kept a control byte: %q", msg.Body)
	}
	stored, err := tr.Store.Messages(tm.ID, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || strings.ContainsAny(stored[0].Body, "\x1b\x07") {
		t.Fatalf("stored post = %+v", stored)
	}
}

func TestSayRefusesTheSenderAndLeavesItOutOfAll(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)

	if _, _, err := tr.Say(tm, "backend", "backend", "me", false); err == nil {
		t.Fatal("a member messaged itself")
	}
	if _, _, err := tr.Say(tm, "backend", "nobody", "hi", false); err == nil {
		t.Fatal("a message to a role that isn't on the team went through")
	}
	_, got, err := tr.Say(tm, "backend", "all", "status?", false)
	if err != nil {
		t.Fatal(err)
	}
	var roles []string
	for _, g := range got {
		roles = append(roles, g.Role)
	}
	if strings.Join(roles, ",") != "lead,frontend" {
		t.Fatalf("all reached %v, want lead and frontend only", roles)
	}
	if len(*sent) != 0 {
		t.Fatalf("typed %+v into members that haven't reported in", *sent)
	}
}

// callerRole is the one role CallerRoles finds, or "".
func callerRole(t *testing.T, tr *Tracker) string {
	t.Helper()
	roles, err := tr.CallerRoles()
	if err != nil {
		t.Fatal(err)
	}
	switch len(roles) {
	case 0:
		return ""
	case 1:
		return roles[0].Team.Name + "/" + roles[0].Member.Role
	}
	t.Fatalf("CallerRoles = %+v, want one", roles)
	return ""
}

func TestCallerRolesFollowTheProcessTree(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	backendUp(t, tr, w)

	// A command in Claude A's shell tool.
	w.self = 200
	if got := callerRole(t, tr); got != "api/backend" {
		t.Fatalf("caller = %q, want backend on api", got)
	}

	// Outside any agent, with the member's environment.
	w.self = 1234
	w.env = map[string]string{"HIVE_TEAM": "t1", "HIVE_ROLE": "frontend"}
	if got := callerRole(t, tr); got != "api/frontend" {
		t.Fatalf("caller by environment = %q, want frontend", got)
	}

	// Outside any agent and any team.
	w.env = map[string]string{}
	if got := callerRole(t, tr); got != "" {
		t.Fatalf("a plain shell is %s", got)
	}
}

func TestCallerRolesTrustTheMembersWindowOverHIVE_PARENT(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	// Claude C made the team and leads it (--lead self); every member's
	// window names it in HIVE_PARENT.
	tr.Store.SetMemberSession("t1", "lead", "claude:C", "")

	// frontend's own session hasn't reported in.
	w.self = 1234
	w.env = map[string]string{"HIVE_PARENT": "claude:C", "HIVE_TEAM": "t1", "HIVE_ROLE": "frontend"}
	if got := callerRole(t, tr); got != "api/frontend" {
		t.Fatalf("caller = %q, want frontend, not the agent that made the team", got)
	}

	// A child of the lead, outside the lead's process, still speaks for it.
	w.env = map[string]string{"HIVE_PARENT": "claude:C"}
	if got := callerRole(t, tr); got != "api/lead" {
		t.Fatalf("caller = %q, want the lead", got)
	}
}

func TestAttentionAndStopArePostedToTheChannel(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	backendUp(t, tr, w)

	claudeHook(t, tr, w, 100, "A", agent.Attention)
	claudeHook(t, tr, w, 100, "A", agent.End)

	if got := hivePosts(tr); got != "backend needs you|backend stopped" {
		t.Fatalf("hive posts = %q", got)
	}
}

func TestASessionOnTwoTeamsGetsMailFromBothInOnePaste(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)
	// A later made a team of its own and leads it (--lead self).
	web := store.Team{ID: "t2", Name: "web", Cwd: "/work", Status: store.TeamActive, LeadRole: "lead", CreatedAt: 10, UpdatedAt: 10}
	tr.Store.CreateTeam(web)
	tr.Store.AddMember(store.Member{TeamID: "t2", Role: "lead", Tool: "claude", SessionID: "claude:A", JoinedAt: 10})
	var woken []string
	tr.OnDeliverable = func(s store.Session) { woken = append(woken, s.ID) }

	claudeHook(t, tr, w, 100, "A", agent.Prompt) // working
	tr.Say(theTeam(t, tr), "lead", "backend", "one", false)
	tr.Say(web, store.FromYou, "lead", "two", false)
	if len(*sent) != 0 {
		t.Fatalf("typed %+v into a working member", *sent)
	}

	claudeHook(t, tr, w, 100, "A", agent.Idle)
	if len(woken) != 1 {
		t.Fatalf("OnDeliverable = %v, want one call for A", woken)
	}
	if how, err := tr.Deliver("claude:A", 0); err != nil || how != "sent" {
		t.Fatalf("Deliver = %q, %v", how, err)
	}
	want := "[web · from you] two\n[api · from lead] one"
	if len(*sent) != 1 || (*sent)[0].text != want {
		t.Fatalf("typed %+v, want one paste %q", *sent, want)
	}
}

func TestDeliverSkipsADoneTeamButStillDeliversAnActiveOne(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)
	claudeHook(t, tr, w, 100, "A", agent.Prompt) // working

	web := store.Team{ID: "t2", Name: "web", Cwd: "/work", Status: store.TeamActive, LeadRole: "lead", CreatedAt: 10, UpdatedAt: 10}
	if err := tr.Store.CreateTeam(web); err != nil {
		t.Fatal(err)
	}
	if err := tr.Store.AddMember(store.Member{TeamID: "t2", Role: "lead", Tool: "claude", SessionID: "claude:A", JoinedAt: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.DoneTeam(web, store.FromYou, false); err != nil {
		t.Fatal(err)
	}
	// Queue mail for the done team directly, and one for the active team.
	msg := store.Message{TeamID: "t2", From: store.FromYou, To: "lead", Body: "web stale", Kind: store.MsgSay, At: 11}
	if _, err := tr.Store.AddMessage(msg, []string{"lead"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tr.Say(theTeam(t, tr), "lead", "backend", "api mail", false); err != nil {
		t.Fatal(err)
	}

	claudeHook(t, tr, w, 100, "A", agent.Idle)
	if _, err := tr.Deliver("claude:A", 0); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 1 || (*sent)[0].text != "[api · from lead] api mail" {
		t.Fatalf("typed %+v, want only the active team's mail", *sent)
	}
	if pending, err := tr.Store.HasPending("t2", "lead"); err != nil || !pending {
		t.Fatalf("done team's mail: pending=%v, %v; want it left queued", pending, err)
	}
}

func TestADoneTeamRefusesTaskMovesAndAddsNoPostOrReadyNote(t *testing.T) {
	tr, _, _ := newTeamWorld(t)
	tm := theTeam(t, tr)
	addTask(t, tr, "first", "")
	addTask(t, tr, "second", "frontend", 1)
	addTask(t, tr, "third", "")
	if _, err := tr.DoneTeam(tm, store.FromYou, false); err != nil {
		t.Fatal(err)
	}
	before, boardBefore := hivePosts(tr), boardText(t, tr, tm)
	_, _, _, done := tr.DoneTask(tm, "lead", 1, "done after the team")
	_, _, block := tr.BlockTask(tm, "lead", 2, "after the team")
	_, edit := tr.EditTask(tm, 3, "renamed", "", "backend", nil)
	for name, err := range map[string]error{"DoneTask": done, "BlockTask": block, "EditTask": edit} {
		if err == nil || !strings.Contains(err.Error(), "api is done") {
			t.Errorf("%s on a done team = %v, want it refused", name, err)
		}
	}
	if got := boardText(t, tr, tm); got != boardBefore {
		t.Errorf("a done team's board changed: %q -> %q", boardBefore, got)
	}
	if got := hivePosts(tr); got != before {
		t.Fatalf("a done team gained task posts: %q -> %q", before, got)
	}
	if pending, err := tr.Store.HasPending(tm.ID, "frontend"); err != nil || pending {
		t.Fatalf("frontend got a ready note from a done team: pending=%v, %v", pending, err)
	}
}

// boardText is each task's number, title, status and owner.
func boardText(t *testing.T, tr *Tracker, tm store.Team) string {
	t.Helper()
	tasks, err := tr.Store.Tasks(tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, task := range tasks {
		out = append(out, fmt.Sprintf("#%d %s %s %s", task.N, task.Title, task.Status, task.Owner))
	}
	return strings.Join(out, "; ")
}

func TestAnExpiredLaunchRecordLinksNothing(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	// hive started the backend in %1 long ago, and it never reported in;
	// since then tmux restarted, and %1 is another agent's.
	stale := w.now - launchTTL.Milliseconds() - 1
	tr.Store.PutLaunchTeam(store.LaunchTeam{Pane: "%1", TeamID: "t1", Role: "backend", CreatedAt: stale})
	claudeHook(t, tr, w, 100, "A", agent.Start)

	if got := roleOf(t, tr, "claude:A"); got != "" {
		t.Fatalf("an unrelated agent became %s", got)
	}
}

func TestClearKeepsTheRoleWhicheverHookComesFirst(t *testing.T) {
	for _, endFirst := range []bool{true, false} {
		tr, w, sent := newTeamWorld(t)
		backendUp(t, tr, w)

		// /clear in Claude: A ends and B starts, in the same process and pane.
		if endFirst {
			claudeHook(t, tr, w, 100, "A", agent.End)
			claudeHook(t, tr, w, 100, "B", agent.Start)
		} else {
			claudeHook(t, tr, w, 100, "B", agent.Start)
			claudeHook(t, tr, w, 100, "A", agent.End)
		}
		if a, b := roleOf(t, tr, "claude:A"), roleOf(t, tr, "claude:B"); a != "" || b != "backend" {
			t.Fatalf("end first %v: A plays %q, B %q; want B the backend", endFirst, a, b)
		}
		if !strings.HasSuffix(hivePosts(tr), "backend continues as claude:B") {
			t.Errorf("end first %v: hive posts = %q", endFirst, hivePosts(tr))
		}
		_, got, _ := tr.Say(theTeam(t, tr), "lead", "backend", "hi", false)
		if len(*sent) != 1 || (*sent)[0].pane != "%1" {
			t.Fatalf("end first %v: typed %+v (%+v), want the message in %%1, not a reopened A", endFirst, *sent, got)
		}
	}
}

func TestAnotherProcessInTheSamePaneDoesNotTakeTheRole(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	backendUp(t, tr, w)

	claudeHook(t, tr, w, 100, "A", agent.End) // the backend quits
	w.add(110, 90, "claude")                  // and the user starts their own Claude in the pane
	claudeHook(t, tr, w, 110, "U", agent.Start)

	if got := roleOf(t, tr, "claude:U"); got != "" {
		t.Fatalf("the user's agent became %s", got)
	}
}

func TestPauseDuringTheSettleStopsTheDelivery(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)
	claudeHook(t, tr, w, 100, "A", agent.Prompt)
	tr.Say(tm, "lead", "backend", "one", false)
	claudeHook(t, tr, w, 100, "A", agent.Idle)

	// The user pauses the team while the delivery waits for the TUI.
	w.onForget = func() {
		if err := tr.SetPaused(tm, true); err != nil {
			t.Fatal(err)
		}
	}
	if how, _ := tr.Deliver("claude:A", time.Millisecond); len(*sent) != 0 || !strings.Contains(how, "paused") {
		t.Fatalf("Deliver = %q, typed %+v; want it queued for the paused team", how, *sent)
	}
}

func TestInboxLeavesAPausedTeamsMail(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)
	claudeHook(t, tr, w, 100, "A", agent.Prompt)
	tr.Say(tm, "lead", "backend", "one", false)
	tr.SetPaused(tm, true)

	if msgs, err := tr.Inbox("t1", "backend"); err != nil || len(msgs) != 0 {
		t.Fatalf("Inbox = %+v, %v; want nothing while paused", msgs, err)
	}
	if pending, _ := tr.Store.HasPending("t1", "backend"); !pending {
		t.Fatal("the paused team's mail was taken")
	}
}

func TestNewTeamIsNotKeptWhenNoMemberStarts(t *testing.T) {
	tr, _ := newWorld(t)
	o := TeamOptions{Name: "api", Cwd: t.TempDir(), Lead: &team.Spec{Tool: "claud"},
		Members: []team.Spec{{Role: "backend", Tool: "codx"}}}
	_, _, err := tr.NewTeam(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "lead: ") || !strings.Contains(err.Error(), "backend: ") {
		t.Fatalf("NewTeam = %v, want why each member failed", err)
	}
	if teams, _ := tr.Store.Teams(); len(teams) != 0 {
		t.Fatalf("teams = %+v, want none kept", teams)
	}

	// The agent that runs the command leads, so the team stands.
	o.Lead, o.SelfLead = nil, &store.Session{ID: "claude:C", Tool: "claude"}
	o.Members = append(o.Members, team.Spec{Role: "api", Tool: "codx"})
	tm, launched, err := tr.NewTeam(context.Background(), o)
	if err != nil || tm.Name != "api" {
		t.Fatalf("NewTeam = %+v, %v; want the name free again", tm, err)
	}
	var roles []string
	for _, l := range launched {
		roles = append(roles, l.Member.Role)
	}
	if got := strings.Join(roles, ","); got != "lead,backend,api" {
		t.Errorf("members started as %s, want the lead first, then in the order given", got)
	}
}

// memberNamed returns the team's member in role.
func memberNamed(t *testing.T, tr *Tracker, tm store.Team, role string) store.Member {
	t.Helper()
	ms, err := tr.Store.Members(tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.Role == role {
			return m
		}
	}
	t.Fatalf("no %s on %s", role, tm.Name)
	return store.Member{}
}

func TestStopTeamStopsAMemberThatHasOnlyAPane(t *testing.T) {
	tr, _, _ := newTeamWorld(t)
	tm := theTeam(t, tr)
	if err := tr.Store.SetMemberPane(tm.ID, "backend", "%1", 90); err != nil {
		t.Fatal(err)
	}
	if got := tr.MemberState(memberNamed(t, tr, tm, "backend"), store.Session{}, false); got != "starting" {
		t.Fatalf("backend in its pane is %q, want starting", got)
	}
	var killed []string
	tr.KillPane = func(pane string) error { killed = append(killed, pane); return nil }

	n, err := tr.StopTeam(tm)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(killed) != 1 || killed[0] != "%1" {
		t.Fatalf("stopped %d, killed %v; want the backend's pane only", n, killed)
	}
	backend := memberNamed(t, tr, tm, "backend")
	if backend.Pane != "" || tr.MemberState(backend, store.Session{}, false) != "stopped" {
		t.Errorf("backend after stop: pane %q, %q; want no pane, stopped", backend.Pane, tr.MemberState(backend, store.Session{}, false))
	}
}

func TestStopKillsOnlyThePaneHiveOpenedForAMember(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	tm := theTeam(t, tr)
	// The backend ran as Claude A in %1 and ended; tmux has since given %1
	// to another pane.
	if err := errors.Join(
		tr.Store.Upsert(store.Session{ID: "claude:A", Tool: "claude", NativeID: "A", Status: store.StatusExited, CreatedAt: 1, UpdatedAt: 1}),
		tr.Store.SetMemberSession(tm.ID, "backend", "claude:A", "%1"),
		// The frontend never reported in, and %2 runs something else now.
		tr.Store.SetMemberPane(tm.ID, "frontend", "%2", 4242),
		// The lead's pane was recorded before hive kept its process.
		tr.Store.SetMemberPane(tm.ID, "lead", "%1", 0),
	); err != nil {
		t.Fatal(err)
	}
	var killed []string
	tr.KillPane = func(pane string) error { killed = append(killed, pane); return nil }

	if n, err := tr.StopTeam(tm); err != nil || n != 0 || len(killed) != 0 {
		t.Fatalf("StopTeam = %d, %v, killed %v; want no pane killed: none is a member's", n, err, killed)
	}
	if roles, err := tr.runningRoles(tm); err != nil || len(roles) != 0 {
		t.Errorf("running roles = %v, %v; want none", roles, err)
	}
	if roles, err := tr.TeamRunning(tm); err != nil || len(roles) != 0 {
		t.Errorf("running members = %v, %v; want none", roles, err)
	}
	if got := tr.MemberState(memberNamed(t, tr, tm, "frontend"), store.Session{}, false); got != "stopped" {
		t.Errorf("frontend is %q, want stopped: its pane is gone", got)
	}
	w.panes = append(w.panes, tmux.Pane{ID: "%9", PID: 4242})
	if err := tr.Store.SetMemberPane(tm.ID, "frontend", "%9", 4242); err != nil {
		t.Fatal(err)
	}
	if roles, err := tr.runningRoles(tm); err != nil || strings.Join(roles, ",") != "frontend" {
		t.Errorf("running roles = %v, %v; want the frontend, in the pane hive opened", roles, err)
	}
}

func TestAMembersPaneIsForgottenWhenItsSessionEnds(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)
	if err := tr.Store.SetMemberPane(tm.ID, "backend", "%1", 90); err != nil {
		t.Fatal(err)
	}
	claudeHook(t, tr, w, 100, "A", agent.End)
	if backend := memberNamed(t, tr, tm, "backend"); backend.Pane != "" || backend.PanePID != 0 {
		t.Errorf("backend after its session ended: pane %q pid %d; want none", backend.Pane, backend.PanePID)
	}
}

func TestRetireMemberReleasesItsClaims(t *testing.T) {
	tr, _, _ := newTeamWorld(t)
	tm := theTeam(t, tr)
	addTask(t, tr, "handlers", "backend")
	addTask(t, tr, "schema", "backend")
	addTask(t, tr, "table", "frontend")
	for _, c := range []struct {
		role string
		n    int
	}{{"backend", 1}, {"frontend", 3}} {
		if _, err := tr.ClaimTask(tm, c.role, c.n, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.RetireMember(tm, "backend"); err != nil {
		t.Fatal(err)
	}
	if got, want := boardText(t, tr, tm), "#1 handlers open ; #2 schema open backend; #3 table claimed frontend"; got != want {
		t.Errorf("board = %q, want %q: only backend's claim reopened", got, want)
	}
	if posts := hivePosts(tr); !strings.Contains(posts, "#1 released: backend left: handlers") || strings.Contains(posts, "#2 released") {
		t.Errorf("posts = %q, want #1 released", posts)
	}
	if _, err := tr.ClaimTask(tm, "frontend", 1, false); err != nil {
		t.Errorf("frontend can't take the released #1: %v", err)
	}
}

func TestRetireMemberStopsAMemberThatHasOnlyAPane(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	tm := theTeam(t, tr)
	w.panes = append(w.panes, tmux.Pane{ID: "%3", PID: 300})
	if err := tr.Store.SetMemberPane(tm.ID, "frontend", "%3", 300); err != nil {
		t.Fatal(err)
	}
	var killed []string
	tr.KillPane = func(pane string) error { killed = append(killed, pane); return nil }

	if err := tr.RetireMember(tm, "frontend"); err != nil {
		t.Fatal(err)
	}
	if len(killed) != 1 || killed[0] != "%3" {
		t.Fatalf("killed %v, want the frontend's pane", killed)
	}
	ms, err := tr.Store.Members(tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.Role == "frontend" && m.Active() {
			t.Fatal("frontend is still active after being retired")
		}
	}
}

func TestNewTeamRefusesAModelItCantPlaceBeforeAnythingStarts(t *testing.T) {
	tr, _ := newWorld(t)
	o := TeamOptions{Name: "api", Cwd: t.TempDir(), Lead: &team.Spec{Tool: "claude", Model: "opus 5.5"},
		Members: []team.Spec{{Role: "backend", Tool: "claude", Model: "opus 5.7"}}}
	_, _, err := tr.NewTeam(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), `backend: claude has no model "opus 5.7"; it runs claude-fable-5-1, claude-opus-5-5`) {
		t.Fatalf("NewTeam = %v, want the member, the model and what claude runs", err)
	}
	if teams, _ := tr.Store.Teams(); len(teams) != 0 {
		t.Fatalf("teams = %+v, want none recorded", teams)
	}
	if _, _, err := tr.AddMember(context.Background(), store.Team{ID: "t", Name: "api", Status: store.TeamActive},
		team.Spec{Role: "qa", Tool: "claude", Model: "opus 5.7"}, ""); err == nil || !strings.Contains(err.Error(), `qa: claude has no model "opus 5.7"`) {
		t.Fatalf("AddMember = %v, want the model refused", err)
	}

	specs := []team.Spec{{Role: "lead", Tool: "claude", Model: "Opus 5.5"}, {Role: "fast", Tool: "claude", Model: "haiku"}}
	if err := tr.resolveModels(context.Background(), specs); err != nil || specs[0].Model != "claude-opus-5-5" || specs[1].Model != "claude-haiku-5-5" {
		t.Errorf("resolveModels = %+v, %v; want the IDs claude takes", specs, err)
	}
}

// Refresh forgets a pane-only member's pane once it is gone, so the board and
// the TUI say stopped, not starting forever. A pane hive still runs keeps its
// record.
func TestRefreshForgetsAMembersPaneOnceItIsGone(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	tm := theTeam(t, tr)
	// frontend's launched pane still runs.
	w.panes = append(w.panes, tmux.Pane{ID: "%3", PID: 300})
	if err := tr.Store.SetMemberPane(tm.ID, "frontend", "%3", 300); err != nil {
		t.Fatal(err)
	}
	// backend's pane is gone.
	if err := tr.Store.SetMemberPane(tm.ID, "backend", "%9", 900); err != nil {
		t.Fatal(err)
	}
	// lead's pane ID was reused by another pane (same ID, another PID).
	if err := tr.Store.SetMemberPane(tm.ID, "lead", "%1", 55); err != nil {
		t.Fatal(err)
	}

	if _, err := tr.Refresh(); err != nil {
		t.Fatal(err)
	}
	members, err := tr.Store.Members(tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range members {
		switch m.Role {
		case "frontend":
			if m.Pane != "%3" || m.PanePID != 300 {
				t.Errorf("frontend lost the pane hive still runs: %+v", m)
			}
		case "backend", "lead":
			if m.Pane != "" || m.PanePID != 0 {
				t.Errorf("%s kept a pane that isn't hive's: %+v", m.Role, m)
			}
		}
	}
}

// When hive can't see tmux (no server, or not on PATH), an empty pane list
// says nothing about the members: keep a pane-only member's record, so team
// stop can still find it. With panes visible but not the member's, clear it.
func TestRefreshKeepsPanesWhenItCannotSeeTmux(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	tm := theTeam(t, tr)
	if err := tr.Store.SetMemberPane(tm.ID, "frontend", "%3", 300); err != nil {
		t.Fatal(err)
	}
	w.panes = nil // tmux isn't there, or the list failed
	if _, err := tr.Refresh(); err != nil {
		t.Fatal(err)
	}
	frontend := memberNamed(t, tr, tm, "frontend")
	if frontend.Pane != "%3" || frontend.PanePID != 300 {
		t.Fatalf("an empty pane list forgot the member's pane: %+v", frontend)
	}
}
