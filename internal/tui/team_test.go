package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
	"github.com/sadrishehu/hive/internal/tracker"
)

// teamOps is the fake world's teams, and what the TUI asked of them.
type teamOps struct {
	snap      TeamSnapshot
	feed      []store.Message
	templates []TeamTemplate

	said        []string // team|to|text
	paused      []string // team|true/false
	led         []string // team|role
	teamStopped []string
	started     []tracker.TeamOptions
	read        []string // team|id
	feedAsked   []string // team

	quotas        map[string]store.Quota
	trashedTeams  []string
	restoredTeams []string
	purgedTeams   []string

	running []string // the roles a fresh look finds running, panes only too

	taskAdded   []taskCall
	taskEdited  []taskCall
	taskDone    []taskCall
	taskBlocked []taskCall
}

// taskCall records what the task editor asked of the board.
type taskCall struct {
	team, title, body, owner, note string
	n                              int
	after                          []int
}

func (f *teamOps) Teams() (TeamSnapshot, error)            { return f.snap, nil }
func (f *teamOps) Quotas() (map[string]store.Quota, error) { return f.quotas, nil }
func (f *teamOps) TeamTemplates() []TeamTemplate           { return f.templates }
func (f *teamOps) TeamFeed(team string, _ int) ([]store.Message, error) {
	f.feedAsked = append(f.feedAsked, team)
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
func (f *teamOps) TeamRunning(team string) ([]string, error) {
	return f.running, nil
}
func (f *teamOps) TrashTeam(tm store.Team) ([]store.Session, error) {
	f.trashedTeams = append(f.trashedTeams, tm.ID)
	return nil, nil
}
func (f *teamOps) RestoreTeam(tm store.Team) ([]store.Session, error) {
	f.restoredTeams = append(f.restoredTeams, tm.ID)
	return nil, nil
}
func (f *teamOps) PurgeTeam(tm store.Team) (tracker.Purged, error) {
	f.purgedTeams = append(f.purgedTeams, tm.ID)
	return tracker.Purged{}, nil
}

func (f *teamOps) TeamTaskAdd(team, title, body, owner string, after []int) (store.Task, error) {
	f.taskAdded = append(f.taskAdded, taskCall{team: team, title: title, body: body, owner: owner, after: after})
	n := 1
	for _, t := range f.snap.Tasks {
		if t.TeamID == team && t.N >= n {
			n = t.N + 1
		}
	}
	task := store.Task{TeamID: team, N: n, Title: title, Body: body, Owner: owner, After: after, Status: store.TaskOpen}
	f.snap.Tasks = append(f.snap.Tasks, task)
	return task, nil
}

func (f *teamOps) TeamTaskEdit(team string, n int, title, body, owner string, after []int) (store.Task, error) {
	f.taskEdited = append(f.taskEdited, taskCall{team: team, n: n, title: title, body: body, owner: owner, after: after})
	task := store.Task{TeamID: team, N: n, Title: title, Body: body, Owner: owner, After: after, Status: store.TaskOpen}
	for i := range f.snap.Tasks {
		if f.snap.Tasks[i].TeamID == team && f.snap.Tasks[i].N == n {
			f.snap.Tasks[i].Title, f.snap.Tasks[i].Body = title, body
			f.snap.Tasks[i].Owner, f.snap.Tasks[i].After = owner, after
			task = f.snap.Tasks[i]
		}
	}
	return task, nil
}

func (f *teamOps) TeamTaskDone(team string, n int, note string) (store.Task, error) {
	f.taskDone = append(f.taskDone, taskCall{team: team, n: n, note: note})
	task := store.Task{TeamID: team, N: n, Status: store.TaskDone, Note: note}
	for i := range f.snap.Tasks {
		if f.snap.Tasks[i].TeamID == team && f.snap.Tasks[i].N == n {
			f.snap.Tasks[i].Status, f.snap.Tasks[i].Note = store.TaskDone, note
			task = f.snap.Tasks[i]
		}
	}
	return task, nil
}

func (f *teamOps) TeamTaskBlock(team string, n int, why string) (store.Task, error) {
	f.taskBlocked = append(f.taskBlocked, taskCall{team: team, n: n, note: why})
	task := store.Task{TeamID: team, N: n, Status: store.TaskBlocked, Note: why}
	for i := range f.snap.Tasks {
		if f.snap.Tasks[i].TeamID == team && f.snap.Tasks[i].N == n {
			f.snap.Tasks[i].Status, f.snap.Tasks[i].Note = store.TaskBlocked, why
			task = f.snap.Tasks[i]
		}
	}
	return task, nil
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
	ops.running = []string{"lead", "backend", "frontend"}
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

func TestAControlSequenceInAPostDoesNotReachTheFeed(t *testing.T) {
	m, _ := setupTeam(t)
	m.feed.msgs = []store.Message{{TeamID: "t1", From: "backend", Kind: store.MsgPost,
		Body: "before\x1b]52;c;bm90IHNhZmU=\x07after"}}
	got := strings.Join(m.feedLines(m.snap.Teams[0], 100), "\n")
	if strings.ContainsAny(got, "\x1b\x07\x00\x9b") {
		t.Fatalf("the feed kept a control byte: %q", got)
	}
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Fatalf("the feed lost the text: %q", got)
	}
}

func TestEnterOnATeamOpensItsChannel(t *testing.T) {
	m, ops := setupTeam(t)
	ops.snap.Tasks = []store.Task{{TeamID: "t1", N: 1, Title: "schema for admin users", Status: store.TaskClaimed, Owner: "backend"}}
	m = step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})

	m = press(t, m, "enter")
	if m.mode != modeChannel || m.channelTeam != "t1" {
		t.Fatalf("mode = %v, channel = %q, want the channel for t1", m.mode, m.channelTeam)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"⬡ api-v2", "add the admin API", "3 members", "● lead", "#1 schema for admin users",
		"start with the schema", "backend → frontend", "message api-v2"} {
		if !strings.Contains(view, want) {
			t.Errorf("channel lacks %q:\n%s", want, view)
		}
	}
	if len(ops.feedAsked) == 0 || ops.feedAsked[len(ops.feedAsked)-1] != "t1" {
		t.Errorf("asked for feeds %v, want t1", ops.feedAsked)
	}
	if m.feed.teamID != "t1" || len(m.feed.msgs) == 0 {
		t.Errorf("feed = %+v, want t1's messages", m.feed)
	}
}

func TestEscLeavesTheChannelForTheTree(t *testing.T) {
	m, _ := setupTeam(t)
	m = press(t, m, "enter")
	m = press(t, m, "esc")
	if m.mode != modeNormal {
		t.Fatalf("mode = %v after esc, want the tree", m.mode)
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "last 24h") || strings.Contains(view, "type a message") {
		t.Errorf("esc didn't bring the tree back:\n%s", view)
	}
}

func TestEnterOnAMemberStillJumps(t *testing.T) {
	m, ops := setupTeam(t)
	m = press(t, m, "enter") // a team opens its channel
	m = press(t, m, "esc")
	m = press(t, m, "j", "j", "j") // frontend
	m = press(t, m, "enter")
	if strings.Join(ops.focused, ",") != "%3" {
		t.Fatalf("focused %v, want the frontend's pane %%3", ops.focused)
	}
}

func TestSendingFromTheChannelStaysOpen(t *testing.T) {
	m, ops := setupTeam(t)
	m = press(t, m, "enter")
	before := len(ops.feedAsked)
	for _, r := range "everyone sync up" {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = press(t, m, "enter")
	if m.mode != modeChannel {
		t.Fatalf("mode = %v after sending, want the channel", m.mode)
	}
	if m.channelInput.Value() != "" {
		t.Errorf("the live line still holds %q", m.channelInput.Value())
	}

	for _, r := range "@backend take the schema" {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = press(t, m, "enter")
	if m.mode != modeChannel {
		t.Fatalf("mode = %v after the second send, want the channel", m.mode)
	}

	want := "t1|all|everyone sync up,t1|backend|take the schema"
	if got := strings.Join(ops.said, ","); got != want {
		t.Fatalf("said = %s\nwant   %s", got, want)
	}
	if len(ops.sent) != 0 {
		t.Errorf("a channel message was typed straight into a session: %v", ops.sent)
	}
	if len(ops.feedAsked) <= before {
		t.Error("sending didn't refresh the feed")
	}
}

func TestALongChannelPromptWraps(t *testing.T) {
	m, ops := setupTeam(t)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = press(t, m, "enter")

	text := "START " + strings.Repeat("a long prompt stays readable ", 6) + "END"
	for _, r := range text {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if rows := m.channelRows(); rows < 2 {
		t.Fatalf("channel rows = %d, want the prompt to take several lines", rows)
	}
	hints := m.hintLines()
	if len(hints) < 2 {
		t.Fatalf("hint lines = %d, want a wrapped live line", len(hints))
	}
	joined := ansi.Strip(strings.Join(hints, "\n"))
	if !strings.Contains(joined, "START") || !strings.Contains(joined, "END") {
		t.Errorf("the live line scrolled its prompt out of view:\n%s", joined)
	}
	for _, line := range hints {
		if width := ansi.StringWidth(line); width > m.width {
			t.Errorf("hint line is %d wide, past the %d screen:\n%s", width, m.width, line)
		}
	}
	if got := len(strings.Split(m.View(), "\n")); got != m.height {
		t.Errorf("view has %d lines, want %d", got, m.height)
	}

	m = press(t, m, "enter")
	if len(ops.said) != 1 || ops.said[0] != "t1|all|"+text {
		t.Fatalf("said = %v, want the whole wrapped prompt", ops.said)
	}
}

func TestHelpNamesTheTeamAndChannelKeys(t *testing.T) {
	m, _ := setupTeam(t)
	m = press(t, m, "?")
	view := ansi.Strip(m.View())
	for _, want := range []string{"e edits its board", "d trashes it", "bare team:", "pgup/pgdn scroll the feed"} {
		if !strings.Contains(view, want) {
			t.Errorf("help lacks %q:\n%s", want, view)
		}
	}
}

func TestChannelArrowsMoveTheCaretAndPageKeysScroll(t *testing.T) {
	m, _ := setupTeam(t)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = press(t, m, "enter")
	for _, r := range strings.Repeat("wrapped words stay editable ", 5) {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	before := cursorRow(m.channelInput)
	if before < 1 {
		t.Fatalf("the prompt didn't wrap; cursor row = %d", before)
	}
	m = step(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if after := cursorRow(m.channelInput); after >= before {
		t.Fatalf("up moved the caret from row %d to %d, want it up", before, after)
	}
	if m.channelScroll != 0 {
		t.Fatalf("typing and moving the caret scrolled the feed to %d", m.channelScroll)
	}
	m = step(t, m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.channelScroll == 0 {
		t.Fatal("pgup didn't scroll the feed")
	}
}

func TestAChannelMarksReadWhileThePreviewIsHidden(t *testing.T) {
	for _, narrow := range []bool{false, true} {
		m, ops := setupTeam(t)
		if narrow {
			m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
		} else {
			m = press(t, m, "tab") // hide the preview
		}
		ops.snap.Unread = map[string]int{"t1": 2}
		m = step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})
		ops.read = nil

		m = press(t, m, "enter")
		if m.mode != modeChannel {
			t.Fatalf("narrow=%v: mode = %v, want the channel", narrow, m.mode)
		}
		if m.snap.Unread["t1"] != 0 {
			t.Errorf("narrow=%v: unread = %v after opening the channel", narrow, m.snap.Unread)
		}
		if len(ops.read) != 1 || ops.read[0] != "t1|"+string(rune('0'+4)) {
			t.Errorf("narrow=%v: read = %v, want it marked up to the last message", narrow, ops.read)
		}
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

// stoppedTeam is a team whose members have all ended, as one goes to the
// trash.
func stoppedTeam(t *testing.T, m Model, ops *fakeOps) Model {
	for i := range ops.sessions {
		ops.sessions[i].Status, ops.sessions[i].PID = store.StatusExited, 0
	}
	ops.running = nil
	return step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})
}

// movedToTrash shows a team in the trash, with its members' sessions.
func movedToTrash(t *testing.T, m Model, ops *fakeOps) Model {
	tm := ops.snap.Teams[0]
	ops.snap.Teams = nil
	ops.snap.Trashed = []store.Team{tm}
	ops.trash = append([]store.Session(nil), ops.sessions...)
	return step(t, m, refreshedMsg{sessions: nil, trashed: ops.trash, snap: ops.snap})
}

func TestATeamGoesToTheTrashWithItsMembers(t *testing.T) {
	m, ops := setupTeam(t)
	m = stoppedTeam(t, m, ops)

	m = press(t, m, "d")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "move team api-v2 and its 3 members to the trash?") {
		t.Fatalf("no confirmation:\n%s", view)
	}
	m = press(t, m, "y")
	if strings.Join(ops.trashedTeams, ",") != "t1" {
		t.Fatalf("trashed teams = %v", ops.trashedTeams)
	}
	if !strings.Contains(m.flash, "moved team api-v2 and its 3 members to the trash") {
		t.Errorf("flash = %q", m.flash)
	}

	m = movedToTrash(t, m, ops)
	m = press(t, m, "t")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "3 sessions, 1 team") || !strings.Contains(view, "in the trash") {
		t.Fatalf("the trash view:\n%s", view)
	}
	if got, want := strings.Join(rowIDs(m), " "), "team:t1 claude:L claude:B codex:F"; got != want {
		t.Fatalf("trash rows = %s, want %s", got, want)
	}
	m = press(t, m, "r")
	if strings.Join(ops.restoredTeams, ",") != "t1" {
		t.Fatalf("restored teams = %v", ops.restoredTeams)
	}
	if !strings.Contains(m.flash, "restored team api-v2 and its 3 members") {
		t.Errorf("flash = %q", m.flash)
	}
}

// A member whose pane hive opened hasn't reported in yet: it still runs, so
// x asks to stop it and d refuses to trash the team until it does.
func TestAPaneOnlyMemberCountsAsRunning(t *testing.T) {
	m, ops := setupTeam(t)
	for i := range ops.sessions {
		ops.sessions[i].Status, ops.sessions[i].PID = store.StatusExited, 0
	}
	ops.snap.Members[1].SessionID = "" // backend hasn't reported in; its pane is there
	ops.running = []string{"backend"}  // as a fresh look at processes and panes finds
	m = step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})

	m = press(t, m, "x")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "stop the 1 running members of api-v2?") {
		t.Fatalf("x didn't count the pane-only member as running:\n%s", view)
	}
	m = press(t, m, "n")
	m = press(t, m, "d")
	if !strings.Contains(m.flash, "stop backend first") {
		t.Fatalf("d flash = %q, want it refused while the pane-only member runs", m.flash)
	}
	if m.mode == modeConfirm {
		t.Error("d asked to trash the team while a member still runs")
	}
}

func TestATeamIsPurgedForGoodFromTheTrash(t *testing.T) {
	m, ops := setupTeam(t)
	m = stoppedTeam(t, m, ops)
	m = movedToTrash(t, m, ops)
	m = press(t, m, "t")

	m = press(t, m, "d")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "delete team api-v2 and its 3 members for good") {
		t.Fatalf("no confirmation:\n%s", view)
	}
	m = press(t, m, "y")
	if strings.Join(ops.purgedTeams, ",") != "t1" {
		t.Fatalf("purged teams = %v", ops.purgedTeams)
	}
	if !strings.Contains(m.flash, "deleted team api-v2 and its 3 members for good") {
		t.Errorf("flash = %q", m.flash)
	}
}

func setTeamRow(m *Model, i int, role, tool, model, prompt string) {
	r := &m.teamForm.members[i]
	r.role.SetValue(role)
	r.tool.SetValue(tool)
	r.model.SetValue(model)
	r.prompt.SetValue(prompt)
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
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "New team") || !strings.Contains(view, "template") || !strings.Contains(view, "role") {
		t.Fatalf("no form:\n%s", view)
	}
	m = typ(m, "docs")
	m = press(t, m, "enter") // folder keeps its default
	m = press(t, m, "enter") // work keeps the shared folder
	m = press(t, m, "enter") // lead keeps claude
	m = press(t, m, "enter") // first member row
	m = typ(m, "writer")
	m = press(t, m, "enter")
	m = typ(m, "codex")
	m = press(t, m, "enter") // model stays blank
	m = press(t, m, "enter")
	m = typ(m, "the docs")
	m = press(t, m, "enter")
	m = typ(m, "write the docs")
	m = press(t, m, "enter")
	if len(ops.started) != 1 {
		t.Fatalf("started = %v", ops.started)
	}
	o := ops.started[0]
	if o.Name != "docs" || o.Goal != "write the docs" || o.Lead == nil || o.Lead.Tool != "claude" ||
		len(o.Members) != 1 || o.Members[0].Role != "writer" || o.Members[0].Tool != "codex" ||
		o.Members[0].Model != "" || o.Members[0].Prompt != "the docs" || o.Worktrees {
		t.Errorf("options = %+v", o)
	}
	if m.mode != modeNormal {
		t.Errorf("mode = %v after starting", m.mode)
	}

	// ←/→ on the work row gives each member a worktree.
	m = press(t, m, "T")
	m = typ(m, "web")
	m = press(t, m, "enter", "enter", "right")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "‹ a worktree per member ›") || !strings.Contains(view, "merges") {
		t.Fatalf("the work row didn't flip:\n%s", view)
	}
	setTeamRow(&m, 0, "writer", "codex", "", "")
	m.teamForm.focus(m.teamForm.goalField())
	m = press(t, m, "enter")
	if len(ops.started) != 2 || !ops.started[1].Worktrees {
		t.Fatalf("started = %+v, want worktrees", ops.started)
	}
}

func TestTeamFormAddsAndRemovesMemberRows(t *testing.T) {
	m, _ := setupTeam(t)
	m = press(t, m, "T")
	if len(m.teamForm.members) != 1 {
		t.Fatalf("started with %d rows, want one", len(m.teamForm.members))
	}
	m = press(t, m, "+")
	if len(m.teamForm.members) != 2 {
		t.Fatalf("after + rows = %d, want two", len(m.teamForm.members))
	}
	if r, c, ok := m.teamForm.rowOf(m.teamForm.field); !ok || r != 1 || c != memberRole {
		t.Fatalf("focus after + = row %d cell %d (%v), want row 1 role", r, c, ok)
	}
	m = press(t, m, "tab")
	if r, c, ok := m.teamForm.rowOf(m.teamForm.field); !ok || r != 1 || c != memberTool {
		t.Fatalf("tab went to row %d cell %d (%v), want row 1 tool", r, c, ok)
	}
	m = press(t, m, "ctrl+d")
	if len(m.teamForm.members) != 1 {
		t.Fatalf("after ctrl+d rows = %d, want one", len(m.teamForm.members))
	}
	m = press(t, m, "ctrl+d")
	if len(m.teamForm.members) != 1 {
		t.Fatalf("ctrl+d removed the only row: %d", len(m.teamForm.members))
	}
}

func TestTeamFormEditsMemberRows(t *testing.T) {
	m, _ := setupTeam(t)
	m = press(t, m, "T")
	m = press(t, m, "tab", "tab", "tab", "tab") // name → folder → work → lead → role
	if _, _, ok := m.teamForm.rowOf(m.teamForm.field); !ok {
		t.Fatalf("tab did not reach the member rows")
	}
	typ := func(m Model, s string) Model {
		for _, r := range s {
			m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		return m
	}
	m = typ(m, "backend")
	m = press(t, m, "tab")
	m = typ(m, "claude")
	m = press(t, m, "tab")
	m = typ(m, "auto")
	m = press(t, m, "tab")
	m = typ(m, "handlers")
	row := m.teamForm.members[0]
	if row.role.Value() != "backend" || row.tool.Value() != "claude" ||
		row.model.Value() != "auto" || row.prompt.Value() != "handlers" {
		t.Errorf("row = %q %q %q %q", row.role.Value(), row.tool.Value(), row.model.Value(), row.prompt.Value())
	}
}

func TestTeamFormOptionsFromRows(t *testing.T) {
	m, _ := setupTeam(t)
	m = press(t, m, "T")
	m.teamForm.name.SetValue("api")
	m.teamForm.folder.SetValue("/src/app")
	m.teamForm.work = true
	setTeamRow(&m, 0, "backend", "claude", "auto", "handlers")
	m.teamForm.addMember()
	setTeamRow(&m, 1, "frontend", "codex", "", "the table")
	o, err := m.teamForm.options()
	if err != nil {
		t.Fatal(err)
	}
	if o.Name != "api" || o.Cwd != "/src/app" || !o.Worktrees || o.Lead == nil || o.Lead.Tool != "claude" {
		t.Errorf("options = %+v", o)
	}
	if len(o.Members) != 2 || o.Members[0].Prompt != "handlers" || o.Members[1].Prompt != "the table" ||
		o.Members[1].Model != "" {
		t.Errorf("members = %+v", o.Members)
	}
}

func TestTeamFormTemplateFillsTheForm(t *testing.T) {
	m, ops := setupTeam(t)
	ops.templates = []TeamTemplate{{
		Name: "api", Goal: "add the admin API", Lead: "codex:gpt-5", Cwd: "~/src/app", Worktrees: true,
		Members: []TeamTemplateMember{
			{Role: "backend", Tool: "claude", Model: "auto", Prompt: "handlers"},
			{Role: "frontend", Tool: "codex"},
		},
	}}
	m = press(t, m, "T")
	m.teamForm.name.SetValue("admin-api")
	m.teamForm.focus(tfTemplate)
	m = press(t, m, "right")
	f := m.teamForm
	if f.name.Value() != "admin-api" {
		t.Errorf("template overwrote the name: %q", f.name.Value())
	}
	if f.folder.Value() != "~/src/app" || !f.work || f.goal.Value() != "add the admin API" {
		t.Errorf("form = folder %q work %v goal %q", f.folder.Value(), f.work, f.goal.Value())
	}
	if len(f.members) != 2 || f.members[0].role.Value() != "backend" ||
		f.members[0].tool.Value() != "claude" || f.members[0].model.Value() != "auto" ||
		f.members[0].prompt.Value() != "handlers" || f.members[1].role.Value() != "frontend" {
		t.Errorf("members = %+v", f.members)
	}
	if f.leads[f.lead] != "codex" {
		t.Errorf("lead = %q, want codex", f.leads[f.lead])
	}
	o, err := f.options()
	if err != nil {
		t.Fatal(err)
	}
	if o.Name != "admin-api" || o.Goal != "add the admin API" || o.Cwd != "~/src/app" || !o.Worktrees ||
		o.Lead == nil || o.Lead.Tool != "codex" {
		t.Errorf("options = %+v", o)
	}
	if len(o.Members) != 2 || o.Members[0].Prompt != "handlers" || o.Members[1].Prompt != "" ||
		o.Members[0].Model != "auto" {
		t.Errorf("members = %+v", o.Members)
	}
}

func TestTeamFormMemberValidation(t *testing.T) {
	m, _ := setupTeam(t)
	m = press(t, m, "T")
	m.teamForm.name.SetValue("api")
	check := func(want string) {
		t.Helper()
		_, err := m.teamForm.options()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want %q", err, want)
		}
	}
	setTeamRow(&m, 0, "", "", "", "")
	check("row 1")
	setTeamRow(&m, 0, "Backend", "claude", "", "")
	check("row 1")
	setTeamRow(&m, 0, "backend", "bad tool", "", "")
	check("row 1")
	setTeamRow(&m, 0, "backend", "claude", "", "")
	if _, err := m.teamForm.options(); err != nil {
		t.Fatalf("a valid row failed: %v", err)
	}
	m.teamForm.addMember()
	setTeamRow(&m, 1, "backend", "codex", "", "")
	check("twice")
	setTeamRow(&m, 1, team.LeadRole, "codex", "", "")
	check("twice")
}

func TestTeamFormNarrowWidth(t *testing.T) {
	m, _ := setupTeam(t)
	m = step(t, m, tea.WindowSizeMsg{Width: 40, Height: 15})
	m = press(t, m, "T")
	for range 8 {
		m = press(t, m, "+")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "New team") || !strings.Contains(view, "role") {
		t.Fatalf("narrow form lost its fields:\n%s", view)
	}
}

func TestTeamFormPicksAModelFromTheToolsList(t *testing.T) {
	m, ops := setupTeam(t)
	m = press(t, m, "T")
	m = press(t, m, "tab", "tab", "tab", "tab", "tab") // name → … → role → tool
	for _, r := range "claude" {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = press(t, m, "tab")
	if _, cell, _ := m.teamForm.rowOf(m.teamForm.field); cell != memberModel {
		t.Fatalf("cursor is in cell %d, want the model", cell)
	}
	if strings.Join(ops.modelsAsked, ",") != "claude" {
		t.Fatalf("asked models of %v, want claude once the cursor is in the model cell", ops.modelsAsked)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"‹ default ›  auto  claude-fable-5-1  claude-opus-5-5", "←/→ picks one, or type an ID or a name like opus 5.5."} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}

	model := func() string { return m.teamForm.members[0].model.Value() }
	var got []string
	for range 4 {
		m = press(t, m, "right")
		got = append(got, model())
	}
	if strings.Join(got, ",") != "auto,claude-fable-5-1,claude-opus-5-5," {
		t.Errorf("→ cycled through %q, want auto, then claude's models, then back to the default", got)
	}
	m = press(t, m, "left")
	if model() != "claude-opus-5-5" {
		t.Errorf("← from the default = %q, want the last model", model())
	}
	if !strings.Contains(ansi.Strip(m.View()), "‹ claude-opus-5-5 ›") {
		t.Errorf("the picker doesn't mark the model in the cell:\n%s", ansi.Strip(m.View()))
	}
	m.teamForm.name.SetValue("api")
	m.teamForm.members[0].role.SetValue("backend")
	o, err := m.teamForm.options()
	if err != nil || o.Members[0].Model != "claude-opus-5-5" {
		t.Errorf("options = %+v, %v; want the picked model", o.Members, err)
	}

	// A name typed isn't a choice: either arrow starts over, and it stays a
	// name for hive to resolve until then.
	m.teamForm.members[0].model.SetValue("opus 5.5")
	if m.teamForm.modelChoice(0) != -1 {
		t.Errorf("a typed name is choice %d, want none", m.teamForm.modelChoice(0))
	}
	m = press(t, m, "left")
	if model() != "claude-opus-5-5" {
		t.Errorf("← from a typed name = %q, want the last choice", model())
	}

	// Moving away and back doesn't ask claude again; a narrow form keeps the
	// picked model in sight.
	m = press(t, m, "tab", "shift+tab")
	if len(ops.modelsAsked) != 1 {
		t.Errorf("asked models %v, want once per tool", ops.modelsAsked)
	}
	help := ansi.Strip(m.teamForm.modelHelp(0, 30)[0])
	if !strings.HasPrefix(help, "…") || !strings.Contains(help, "‹ claude-opus-5-5 ›") {
		t.Errorf("narrow picker = %q, want it scrolled to the model in the cell", help)
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

func TestTeamFilterShowsTheWholeTeamSubtree(t *testing.T) {
	m, ops := setupTeam(t)
	ops.sessions = append(ops.sessions,
		store.Session{ID: "claude:LS", Tool: "claude", Title: "look things up", ParentID: "claude:L", Kind: "internal",
			Status: "working", CreatedAt: ago(20 * time.Minute), UpdatedAt: ago(time.Minute)},
		store.Session{ID: "claude:W", Tool: "claude", Title: "web polish", Status: "working", PID: 21, Pane: "%4",
			CreatedAt: ago(25 * time.Minute), UpdatedAt: ago(time.Minute)},
		store.Session{ID: "claude:Z", Tool: "claude", Title: "standalone", Status: "idle", PID: 22, Pane: "%5",
			CreatedAt: ago(24 * time.Minute), UpdatedAt: ago(2 * time.Minute)},
	)
	ops.snap.Teams = append(ops.snap.Teams, store.Team{ID: "t2", Name: "web", Status: store.TeamActive,
		CreatedAt: ago(25 * time.Minute), UpdatedAt: ago(time.Minute)})
	ops.snap.Members = append(ops.snap.Members, store.Member{TeamID: "t2", Role: "writer", SessionID: "claude:W", Tool: "claude", JoinedAt: 4})
	m = step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})

	m = press(t, m, "/", "t", "e", "a", "m", ":", "A", "P", "I", "enter")
	if got, want := strings.Join(rowIDs(m), " "), "team:t1 claude:L claude:LS claude:B codex:F"; got != want {
		t.Fatalf("team:API rows = %s, want %s", got, want)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "team “api”") {
		t.Errorf("the header doesn't name the team scope:\n%s", view)
	}

	m = press(t, m, "esc")
	rows := map[string]bool{}
	for _, id := range rowIDs(m) {
		rows[id] = true
	}
	for _, id := range []string{"team:t1", "claude:L", "claude:LS", "claude:B", "codex:F", "team:t2", "claude:W", "claude:Z"} {
		if !rows[id] {
			t.Errorf("esc left out %s", id)
		}
	}

	// A bare team: shows every team, with its subtree, and no plain session.
	m = press(t, m, "/", "t", "e", "a", "m", ":", "enter")
	rows = map[string]bool{}
	for _, id := range rowIDs(m) {
		rows[id] = true
	}
	for _, id := range []string{"team:t1", "claude:L", "claude:LS", "claude:B", "codex:F", "team:t2", "claude:W"} {
		if !rows[id] {
			t.Errorf("team: left out %s", id)
		}
	}
	if rows["claude:Z"] {
		t.Error("team: kept a session that is on no team")
	}

	m = press(t, m, "esc", "/", "t", "e", "a", "m", ":", "z", "z", "z", "enter")
	if len(m.rows) != 0 || !strings.Contains(ansi.Strip(m.View()), "nothing matches") {
		t.Fatalf("no match: %d rows\n%s", len(m.rows), ansi.Strip(m.View()))
	}

	m = press(t, m, "esc")
	if len(m.rows) == 0 {
		t.Fatal("esc didn't clear the filter")
	}

	// Plain text still matches sessions and keeps the ancestors that lead to them.
	m = press(t, m, "/", "h", "a", "n", "d", "l", "e", "r", "s", "enter")
	if got, want := strings.Join(rowIDs(m), " "), "team:t1 claude:B"; got != want {
		t.Fatalf("plain filter rows = %s, want %s", got, want)
	}
}

func TestTeamFilterWorksInTheTrash(t *testing.T) {
	m, ops := setupTeam(t)
	trashed := teamSessions()
	for i := range trashed {
		trashed[i].DeletedAt = ago(time.Hour)
	}
	ops.trash = append(trashed,
		store.Session{ID: "claude:W", Tool: "claude", Title: "web polish", Status: "exited",
			CreatedAt: ago(2 * time.Hour), UpdatedAt: ago(time.Hour), DeletedAt: ago(time.Hour)},
		store.Session{ID: "claude:Z", Tool: "claude", Title: "old spike", Status: "exited",
			CreatedAt: ago(2 * time.Hour), UpdatedAt: ago(time.Hour), DeletedAt: ago(time.Hour)},
	)
	ops.sessions = nil
	ops.snap.Teams = nil
	ops.snap.Trashed = []store.Team{
		{ID: "t1", Name: "api-v2", Status: store.TeamActive, CreatedAt: ago(30 * time.Minute), UpdatedAt: ago(time.Minute)},
		{ID: "t2", Name: "web", Status: store.TeamActive, CreatedAt: ago(25 * time.Minute), UpdatedAt: ago(time.Minute)},
	}
	ops.snap.Members = append(ops.snap.Members, store.Member{TeamID: "t2", Role: "writer", SessionID: "claude:W", Tool: "claude", JoinedAt: 4})
	m = step(t, m, refreshedMsg{sessions: ops.sessions, trashed: ops.trash, snap: ops.snap})
	m = press(t, m, "t")

	m = press(t, m, "/", "t", "e", "a", "m", ":", "enter")
	rows := map[string]bool{}
	for _, id := range rowIDs(m) {
		rows[id] = true
	}
	for _, id := range []string{"team:t1", "claude:L", "claude:B", "codex:F", "team:t2", "claude:W"} {
		if !rows[id] {
			t.Errorf("team: in the trash left out %s", id)
		}
	}
	if rows["claude:Z"] {
		t.Error("team: in the trash kept a session that is on no team")
	}

	m = press(t, m, "esc", "/", "t", "e", "a", "m", ":", "t", "2", "enter")
	if got, want := strings.Join(rowIDs(m), " "), "team:t2 claude:W"; got != want {
		t.Fatalf("team:t2 in the trash = %s, want %s", got, want)
	}
}

// The message prompt on a team wraps as it grows, like the channel's: the
// whole text stays visible and is what hive sends.
func TestALongSendPromptWraps(t *testing.T) {
	m, ops := setupTeam(t)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = press(t, m, "s")

	text := "START " + strings.Repeat("a long message stays readable ", 6) + "END"
	for _, r := range text {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if rows := m.sendRows(); rows < 2 {
		t.Fatalf("send rows = %d, want the prompt to take several lines", rows)
	}
	hints := m.hintLines()
	joined := ansi.Strip(strings.Join(hints, "\n"))
	if !strings.Contains(joined, "START") || !strings.Contains(joined, "END") {
		t.Errorf("the send prompt scrolled its text out of view:\n%s", joined)
	}
	for _, line := range hints {
		if width := ansi.StringWidth(line); width > m.width {
			t.Errorf("hint line is %d wide, past the %d screen:\n%s", width, m.width, line)
		}
	}
	if got := len(strings.Split(m.View(), "\n")); got != m.height {
		t.Errorf("view has %d lines, want %d", got, m.height)
	}

	m = press(t, m, "enter")
	if len(ops.said) != 1 || !strings.HasSuffix(ops.said[0], "|"+text) {
		t.Fatalf("said = %v, want the whole wrapped prompt", ops.said)
	}
}

// The new-team form's fields wrap as they grow: a long name, folder and
// member part keep their start and end on screen, the screen keeps its
// height, and the whole part is what starts.
func TestALongNewTeamFormWraps(t *testing.T) {
	m, ops := setupTeam(t)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = press(t, m, "T")
	typ := func(s string) {
		for _, r := range s {
			m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
	check := func(what string) {
		t.Helper()
		view := ansi.Strip(m.View())
		if !strings.Contains(view, "START") || !strings.Contains(view, "END") {
			t.Errorf("%s: the form scrolled its text out of view:\n%s", what, view)
		}
		for _, line := range strings.Split(view, "\n") {
			if width := ansi.StringWidth(line); width > m.width {
				t.Errorf("%s: a line is %d wide, past the %d screen:\n%s", what, width, m.width, line)
			}
		}
		if got := len(strings.Split(view, "\n")); got != m.height {
			t.Errorf("%s: view has %d lines, want %d", what, got, m.height)
		}
		if n := len(m.teamFormLines(m.width, m.height)); n != m.height {
			t.Errorf("%s: form is %d lines, want %d", what, n, m.height)
		}
	}

	typ("START " + strings.Repeat("a team name stays readable ", 4) + "END")
	check("name")

	m.teamForm.name.SetValue("wrap")
	m.teamForm.folder.SetValue("START " + strings.Repeat("a folder path stays readable ", 4) + "END")
	m.teamForm.folder.CursorEnd()
	m.teamForm.focus(tfFolder)
	check("folder")

	part := "START " + strings.Repeat("the part wraps too ", 4) + "END"
	m.teamForm.folder.SetValue(t.TempDir())
	m.teamForm.focus(m.teamForm.membersAt() + memberPart)
	typ(part)
	check("part")

	m.teamForm.name.SetValue("wrap")
	m.teamForm.members[0].role.SetValue("backend")
	m.teamForm.members[0].tool.SetValue("claude")
	m.teamForm.focus(m.teamForm.goalField())
	m = press(t, m, "enter")
	if len(ops.started) != 1 {
		t.Fatalf("started = %+v", ops.started)
	}
	if got := ops.started[0].Members[0].Prompt; got != part {
		t.Fatalf("part = %q, want the whole wrapped text", got)
	}
}

// Up and down move the caret through a wrapped team goal; tab still moves
// between fields.
func TestUpDownMoveTheCaretInsideTheTeamGoal(t *testing.T) {
	m, _ := setupTeam(t)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = press(t, m, "T")
	m.teamForm.focus(m.teamForm.goalField())
	for _, r := range strings.Repeat("the team goal wraps and stays editable ", 6) {
		m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	before := cursorRow(m.teamForm.goal)
	if before < 1 {
		t.Fatalf("the goal didn't wrap; cursor row = %d", before)
	}
	m = press(t, m, "up")
	if after := cursorRow(m.teamForm.goal); after >= before {
		t.Fatalf("up moved the caret from row %d to %d, want it up", before, after)
	}
	if m.teamForm.field != m.teamForm.goalField() {
		t.Fatalf("up left the goal for field %d, want the caret to move, not the focus", m.teamForm.field)
	}
	m = press(t, m, "tab")
	if m.teamForm.field != tfTemplate {
		t.Fatalf("tab left the goal for field %d, want around to the template", m.teamForm.field)
	}
}

// A long team goal and a long member part wrap in the channel head instead of
// running off the edge, and the view keeps its height.
func TestALongTeamGoalAndPartWrap(t *testing.T) {
	m, ops := setupTeam(t)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	ops.snap.Teams[0].Goal = "START " + strings.Repeat("what the team is for ", 5) + "END"
	ops.snap.Members[0].Prompt = "START " + strings.Repeat("what this member owns ", 5) + "END"
	m = step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})
	m = press(t, m, "enter")

	view := ansi.Strip(m.View())
	for _, want := range []string{"START", "END"} {
		if strings.Count(view, want) < 2 {
			t.Errorf("the channel lacks the goal's and the part's %q:\n%s", want, view)
		}
	}
	for _, line := range strings.Split(view, "\n") {
		if width := ansi.StringWidth(line); width > m.width {
			t.Errorf("a line is %d wide, past the %d screen:\n%s", width, m.width, line)
		}
	}
	if got := len(strings.Split(view, "\n")); got != m.height {
		t.Errorf("view has %d lines, want %d", got, m.height)
	}
}

// With several short members, the form draws every row rather than counting
// each wrapping cell as four lines. A long part still wraps.
func TestAShortTeamFormShowsEveryMember(t *testing.T) {
	m, _ := setupTeam(t)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = press(t, m, "T")
	for range 4 {
		m = press(t, m, "+")
	}
	roles := []string{"one", "two", "three", "four", "five"}
	for i, role := range roles {
		setTeamRow(&m, i, role, "claude", "", "")
	}
	view := ansi.Strip(m.View())
	for _, role := range roles {
		if !strings.Contains(view, role) {
			t.Errorf("member %q isn't shown:\n%s", role, view)
		}
	}
	m.teamForm.members[0].prompt.SetValue("START " + strings.Repeat("a long part stays readable ", 4) + "END")
	m.teamForm.focus(m.teamForm.membersAt() + memberPart)
	view = ansi.Strip(m.View())
	if !strings.Contains(view, "START") || !strings.Contains(view, "END") {
		t.Errorf("the form cut a long part:\n%s", view)
	}
	if got := len(strings.Split(view, "\n")); got != m.height {
		t.Errorf("view has %d lines, want %d", got, m.height)
	}
}

// The channel shows each member's state, model, session and context, and the
// account's rate limits once, not once per member.
func TestTheChannelShowsMembersUsageAndTheAccountsQuota(t *testing.T) {
	m, ops := setupTeam(t)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	ops.usages = map[string]store.Usage{
		"claude:L": {ID: "claude:L", Model: "claude-opus-5-5", Effort: "high",
			ContextTokens: 188_000, ContextWindow: 1_000_000},
		"claude:B": {ID: "claude:B", Model: "claude-sonnet-5-5",
			ContextTokens: 42_000, ContextWindow: 200_000},
	}
	ops.quotas = map[string]store.Quota{"claude": {Tool: "claude", FiveUsed: 7,
		FiveReset: now.Add(4*time.Hour + 54*time.Minute).Unix(), SevenUsed: 22, At: now.Unix()}}
	m = press(t, m, "enter")
	m = step(t, m, tickMsg(now))

	view := ansi.Strip(m.View())
	for _, want := range []string{
		"claude-opus-5-5", "high", "188k/1M", "19%",
		"claude-sonnet-5-5", "42k/200k", "21%",
		"5h 7% · resets 4h54m · 7d 22%",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the channel lacks %q:\n%s", want, view)
		}
	}
	if n := strings.Count(view, "5h 7%"); n != 1 {
		t.Errorf("the account quota shows %d times, want once:\n%s", n, view)
	}
	for _, line := range strings.Split(view, "\n") {
		if width := ansi.StringWidth(line); width > m.width {
			t.Errorf("a line is %d wide, past the %d screen:\n%s", width, m.width, line)
		}
	}
	if got := len(strings.Split(view, "\n")); got != m.height {
		t.Errorf("view has %d lines, want %d", got, m.height)
	}
}

// A tool with no reported rate limits shows no number, never a wrong one.
func TestTheChannelShowsNoQuotaWhenItCannotKnow(t *testing.T) {
	m, ops := setupTeam(t)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	// Both windows reset in the past: their old percentages mean nothing now.
	ops.quotas = map[string]store.Quota{"claude": {Tool: "claude", FiveUsed: 92,
		FiveReset: now.Add(-time.Hour).Unix(), SevenUsed: 30, SevenReset: now.Add(-time.Minute).Unix()}}
	m = press(t, m, "enter")
	m = step(t, m, tickMsg(now))
	view := ansi.Strip(m.View())
	for _, bad := range []string{"5h ", "7d ", "resets"} {
		if strings.Contains(view, bad) {
			t.Errorf("the channel showed a stale quota (%q):\n%s", bad, view)
		}
	}
}

// Only a team's channel or its tree preview needs every member's usage and
// the account quota; the plain tree keeps the selected session's own fetch.
func TestOnlyATeamInViewFetchesMemberUsage(t *testing.T) {
	m, ops := setup(t, false)
	m.lastSync = now
	before := ops.usagesAsked
	m = step(t, m, tickMsg(now))
	if ops.usagesAsked != before {
		t.Errorf("the plain tree fetched member usages %d times", ops.usagesAsked-before)
	}

	m, ops = setupTeam(t)
	ops.usagesAsked = 0
	m.lastSync = now
	m.quotasAt = time.Time{}
	m = step(t, m, tickMsg(now))
	if ops.usagesAsked == 0 {
		t.Error("the team preview didn't fetch the members' usage")
	}
}

// memberState matches the tracker's MemberState from the member record alone:
// a live pane keeps a reported-nothing member starting, and a cleared pane
// (the way #28 leaves it) means stopped.
func TestAMembersStateMatchesTheTracker(t *testing.T) {
	live := store.Member{TeamID: "t1", Role: "lead", SessionID: "claude:L"}
	attention := store.Member{TeamID: "t1", Role: "ui", SessionID: "claude:F"}
	ended := store.Member{TeamID: "t1", Role: "done", SessionID: "claude:E"}
	pane := store.Member{TeamID: "t1", Role: "pane", Pane: "%7", PanePID: 4242}
	stopped := store.Member{TeamID: "t1", Role: "stopped"}
	left := store.Member{TeamID: "t1", Role: "gone", LeftAt: 1}
	for _, c := range []struct {
		name  string
		mem   store.Member
		s     store.Session
		found bool
		want  string
	}{
		{"working", live, store.Session{ID: "claude:L", Status: store.StatusWorking, PID: 11}, true, "working"},
		{"attention", attention, store.Session{ID: "claude:F", Status: store.StatusAttention, PID: 12}, true, "needs you"},
		{"ended", ended, store.Session{ID: "claude:E", Status: store.StatusExited}, true, "ended"},
		{"pane only", pane, store.Session{}, false, "starting"},
		{"stopped", stopped, store.Session{}, false, "stopped"},
		{"left", left, store.Session{}, false, "left"},
	} {
		if got := memberState(c.mem, c.s, c.found); got != c.want {
			t.Errorf("%s: memberState = %q, want %q", c.name, got, c.want)
		}
	}
}

// The channel draws those states: a pane-only member is starting, one hive
// has cleared is stopped.
func TestTheChannelShowsStartingAndStoppedMembers(t *testing.T) {
	m, ops := setupTeam(t)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	ops.snap.Members = []store.Member{
		{TeamID: "t1", Role: "pane", Tool: "claude", Pane: "%7", PanePID: 4242, JoinedAt: 1},
		{TeamID: "t1", Role: "gone", Tool: "claude", JoinedAt: 2},
	}
	m = step(t, m, refreshedMsg{sessions: nil, snap: ops.snap})
	m = press(t, m, "enter")

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "pane · starting") {
		t.Errorf("a pane-only member isn't starting:\n%s", view)
	}
	if !strings.Contains(view, "gone · stopped") {
		t.Errorf("a cleared member isn't stopped:\n%s", view)
	}
}

// With showUsage on and a narrow screen the usage panel takes the body and a
// selected team's head is drawn there, so its usage must be fetched too.
func TestATeamHeadInTheUsageBodyIsFetched(t *testing.T) {
	m, ops := setupTeam(t)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.showUsage = true
	if !m.usageTakesTheBody() {
		t.Fatalf("setup: the usage panel doesn't take the body (preview %v)", m.previewVisible())
	}
	if !m.teamUsageInView() {
		t.Fatal("a team head drawn in the usage body isn't in view")
	}
	m.lastSync = now
	ops.usagesAsked = 0
	m.quotasAt = time.Time{}
	m = step(t, m, tickMsg(now))
	if ops.usagesAsked == 0 {
		t.Error("the team head in the usage body didn't fetch usage")
	}

	// A plain session in the usage body has no team head: no member fetch.
	m, ops = setup(t, false)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.showUsage = true
	if !m.usageTakesTheBody() {
		t.Fatal("setup: the usage panel doesn't take the body for a session")
	}
	if m.teamUsageInView() {
		t.Fatal("a plain session's usage body counts as a team view")
	}
	m.lastSync = now
	ops.usagesAsked = 0
	m.quotasAt = time.Time{}
	m = step(t, m, tickMsg(now))
	if ops.usagesAsked != 0 {
		t.Errorf("a plain session fetched every usage %d times", ops.usagesAsked)
	}
}

// The new-team form is a full form, not usage: with the usage panel on and a
// narrow window it must still show over the body.
func TestTheNewTeamFormShowsOverTheUsageBody(t *testing.T) {
	m, _ := setupTeam(t)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.showUsage = true
	m = press(t, m, "T")
	if m.mode != modeTeam {
		t.Fatalf("T opened mode %v, want the new-team form", m.mode)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"New team", "goal", "what the team is for"} {
		if !strings.Contains(view, want) {
			t.Errorf("the usage body hid the form (%q missing):\n%s", want, view)
		}
	}
}
