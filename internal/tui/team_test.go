package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
)

// teamOps is the fake world's teams, and what the TUI asked of them.
type teamOps struct {
	snap TeamSnapshot
	feed []store.Message

	said        []string // team|to|text
	paused      []string // team|true/false
	led         []string // team|role
	teamStopped []string
	started     []tracker.TeamOptions
	read        []string // team|id
}

func (f *teamOps) Teams() (TeamSnapshot, error) { return f.snap, nil }
func (f *teamOps) TeamFeed(string, int) ([]store.Message, error) {
	return f.feed, nil
}
func (f *teamOps) TeamSay(team, to, text string) (string, error) {
	f.said = append(f.said, team+"|"+to+"|"+text)
	return to + ": sent", nil
}
func (f *teamOps) TeamPause(team string, paused bool) error {
	f.paused = append(f.paused, team+"|"+map[bool]string{true: "true", false: "false"}[paused])
	return nil
}
func (f *teamOps) TeamLead(team, role string) error { f.led = append(f.led, team+"|"+role); return nil }
func (f *teamOps) TeamStop(team string) (int, error) {
	f.teamStopped = append(f.teamStopped, team)
	return 3, nil
}
func (f *teamOps) TeamStart(o tracker.TeamOptions) (string, error) {
	f.started = append(f.started, o)
	return "team " + o.Name + ": started", nil
}
func (f *teamOps) TeamRead(team string, id int64) error {
	f.read = append(f.read, team+"|"+string(rune('0'+id)))
	return nil
}

// teamSessions is a lead at work, an idle backend and a frontend that needs
// the user, in panes of their own.
func teamSessions() []store.Session {
	return []store.Session{
		{ID: "claude:L", Tool: "claude", NativeID: "L", Title: "planning the API", Cwd: "/src/app", Kind: "interactive",
			Status: "working", PID: 11, Pane: "%1", CreatedAt: ago(30 * time.Minute), UpdatedAt: ago(time.Minute)},
		{ID: "claude:B", Tool: "claude", NativeID: "B", Title: "handlers", Cwd: "/src/app", Kind: "interactive",
			Status: "idle", PID: 12, Pane: "%2", CreatedAt: ago(29 * time.Minute), UpdatedAt: ago(2 * time.Minute)},
		{ID: "codex:F", Tool: "codex", NativeID: "F", Title: "table view", Cwd: "/src/app", Kind: "interactive",
			Status: "attention", StatusAt: ago(5 * time.Minute), PID: 13, Pane: "%3", CreatedAt: ago(28 * time.Minute), UpdatedAt: ago(5 * time.Minute)},
	}
}

func teamSnapshot() TeamSnapshot {
	return TeamSnapshot{
		Teams: []store.Team{{ID: "t1", Name: "api-v2", Goal: "add the admin API", Cwd: "/src/app", Status: store.TeamActive,
			LeadRole: "lead", CreatedAt: ago(30 * time.Minute), UpdatedAt: ago(time.Minute)}},
		Members: []store.Member{
			{TeamID: "t1", Role: "lead", SessionID: "claude:L", Tool: "claude", JoinedAt: 1},
			{TeamID: "t1", Role: "backend", SessionID: "claude:B", Tool: "claude", JoinedAt: 2},
			{TeamID: "t1", Role: "frontend", SessionID: "codex:F", Tool: "codex", JoinedAt: 3},
		},
		Unread: map[string]int{"t1": 2},
	}
}

func teamFeed() []store.Message {
	return []store.Message{
		{ID: 1, TeamID: "t1", From: store.FromYou, To: "lead", Body: "start with the schema", Kind: store.MsgSay, At: ago(25 * time.Minute)},
		{ID: 2, TeamID: "t1", From: "lead", Body: "Plan is up: backend takes handlers, frontend the table.", Kind: store.MsgPost, At: ago(20 * time.Minute)},
		{ID: 3, TeamID: "t1", From: store.FromHive, Body: "frontend needs you", Kind: store.MsgSystem, At: ago(5 * time.Minute)},
		{ID: 4, TeamID: "t1", From: "backend", To: "frontend", Body: "the list endpoint is GET /admin/users?page=", Kind: store.MsgSay, At: ago(time.Minute)},
	}
}

func setupTeam(t *testing.T) (Model, *fakeOps) {
	t.Helper()
	ops := &fakeOps{sessions: teamSessions()}
	ops.snap, ops.feed = teamSnapshot(), teamFeed()
	m := New(ops, false)
	m.now = func() time.Time { return now }
	m = step(t, m, tea.WindowSizeMsg{Width: 160, Height: 30})
	m = step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})
	return m, ops
}

func TestTeamIsANodeWithItsMembersUnderIt(t *testing.T) {
	m, ops := setupTeam(t)
	ops.snap.Unread = map[string]int{"t1": 2}
	if got, want := strings.Join(rowIDs(m), " "), "team:t1 claude:L claude:B codex:F"; got != want {
		t.Fatalf("rows = %s, want %s", got, want)
	}
	// The team is selected, so its channel is read; another row shows what is new.
	m = press(t, m, "j")
	m = step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})
	view := ansi.Strip(m.View())
	for _, want := range []string{"1 team", "api-v2", "add the admin API", "3 members", "2 new",
		"lead · planning the API", "backend · handlers", "frontend · table view"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	// The team's own members aren't counted as extra sessions.
	if !strings.Contains(view, "3 sessions") {
		t.Errorf("header should count the 3 real sessions:\n%s", view)
	}
}

func TestSelectingATeamShowsItsChannelAndMarksItRead(t *testing.T) {
	m, ops := setupTeam(t)
	view := ansi.Strip(m.View())
	for _, want := range []string{"⬡ api-v2", "led by lead", "start with the schema", "you → lead",
		"backend → frontend", "frontend needs you", "frontend waits on you"} {
		if !strings.Contains(view, want) {
			t.Errorf("channel lacks %q:\n%s", want, view)
		}
	}
	if len(ops.read) != 1 || ops.read[0] != "t1|"+string(rune('0'+4)) {
		t.Errorf("read = %v, want the channel marked read up to its last message", ops.read)
	}
	if m.snap.Unread["t1"] != 0 {
		t.Errorf("unread = %v after reading", m.snap.Unread)
	}

	m = press(t, m, "m") // hide hive's own posts
	if view := ansi.Strip(m.View()); strings.Contains(view, "frontend needs you") && strings.Count(view, "frontend needs you") > 1 {
		t.Errorf("m left hive's posts in the channel:\n%s", view)
	}
	if strings.Contains(ansi.Strip(strings.Join(m.feedLines(m.snap.Teams[0], 80), "\n")), "hive") {
		t.Error("m left hive's own posts in the feed")
	}
}

func TestEnterOnATeamJumpsToTheMemberThatNeedsYou(t *testing.T) {
	m, ops := setupTeam(t)
	press(t, m, "enter")
	if strings.Join(ops.focused, ",") != "%3" {
		t.Fatalf("focused %v, want the frontend's pane %%3", ops.focused)
	}

	// Nobody waits: the lead.
	ops2 := &fakeOps{sessions: teamSessions()}
	ops2.sessions[2].Status = "idle"
	ops2.snap, ops2.feed = teamSnapshot(), teamFeed()
	m2 := New(ops2, false)
	m2.now = func() time.Time { return now }
	m2 = step(t, m2, tea.WindowSizeMsg{Width: 160, Height: 30})
	m2 = step(t, m2, refreshedMsg{sessions: ops2.sessions, snap: ops2.snap})
	press(t, m2, "enter")
	if strings.Join(ops2.focused, ",") != "%1" {
		t.Fatalf("focused %v, want the lead's pane %%1", ops2.focused)
	}
}

func TestSendOnATeamGoesThroughTheMailbox(t *testing.T) {
	m, ops := setupTeam(t)

	m = press(t, m, "s")
	for _, r := range "everyone sync up" {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = press(t, m, "enter")
	m = press(t, m, "s")
	for _, r := range "@backend take the schema" {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = press(t, m, "enter")

	// On a member row, the message is for that role.
	m = press(t, m, "j", "j", "s") // backend
	for _, r := range "ship it" {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = press(t, m, "enter")

	want := "t1|all|everyone sync up,t1|backend|take the schema,t1|backend|ship it"
	if got := strings.Join(ops.said, ","); got != want {
		t.Fatalf("said = %s\nwant   %s", got, want)
	}
	if len(ops.sent) != 0 {
		t.Errorf("a team message was typed straight into a session: %v", ops.sent)
	}
}

func TestPauseLeadAndStopOnATeam(t *testing.T) {
	m, ops := setupTeam(t)

	m = press(t, m, "p")
	m = press(t, m, "L") // the lead is an agent: the user takes over
	if strings.Join(ops.paused, ",") != "t1|true" || strings.Join(ops.led, ",") != "t1|" {
		t.Fatalf("paused %v led %v", ops.paused, ops.led)
	}

	// With the user leading, L hands it to the member called lead.
	ops.snap.Teams[0].LeadRole = ""
	m = step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})
	m = press(t, m, "L")
	if got := strings.Join(ops.led, ","); got != "t1|,t1|lead" {
		t.Fatalf("led = %s, want the user then lead", got)
	}

	m = press(t, m, "x")
	if !strings.Contains(ansi.Strip(m.View()), "stop the 3 running members of api-v2?") {
		t.Fatalf("no confirmation:\n%s", ansi.Strip(m.View()))
	}
	m = press(t, m, "y")
	if strings.Join(ops.teamStopped, ",") != "t1" {
		t.Fatalf("stopped = %v", ops.teamStopped)
	}
	if !strings.Contains(m.flash, "stopped 3 members of api-v2") {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestNewTeamForm(t *testing.T) {
	m, ops := setupTeam(t)
	typ := func(m Model, s string) Model {
		for _, r := range s {
			m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		return m
	}

	m = press(t, m, "T")
	if !strings.Contains(ansi.Strip(m.View()), "New team") {
		t.Fatalf("no form:\n%s", ansi.Strip(m.View()))
	}
	m = typ(m, "docs")
	m = press(t, m, "enter") // folder keeps its default
	m = press(t, m, "enter") // lead keeps claude
	m = press(t, m, "enter") // members
	m = typ(m, "writer=codex bad role")
	m = press(t, m, "enter", "enter") // an invalid member: the form stays
	if len(ops.started) != 0 || m.mode != modeTeam {
		t.Fatalf("started %v from an invalid form (mode %v)", ops.started, m.mode)
	}
	if !strings.Contains(m.flash, "role") {
		t.Errorf("flash = %q, want the reason", m.flash)
	}

	m.teamForm.members.SetValue("writer=codex reviewer=opencode:auto")
	m.teamForm.focus(tfGoal)
	m = typ(m, "write the docs")
	m = press(t, m, "enter")
	if len(ops.started) != 1 {
		t.Fatalf("started = %v", ops.started)
	}
	o := ops.started[0]
	if o.Name != "docs" || o.Goal != "write the docs" || o.Lead == nil || o.Lead.Tool != "claude" ||
		len(o.Members) != 2 || o.Members[1].Model != "auto" {
		t.Errorf("options = %+v", o)
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v after starting", m.mode)
	}
}

func TestTeamKeysLeaveOrdinarySessionsAlone(t *testing.T) {
	m, ops := setup(t, false)
	m = press(t, m, "p", "L", "m", "x")
	if len(ops.paused) != 0 || len(ops.led) != 0 {
		t.Errorf("team keys acted on a plain session: %v %v", ops.paused, ops.led)
	}
	if m.mode != modeConfirm {
		t.Errorf("x should still ask to stop a plain session, mode = %v", m.mode)
	}
}
