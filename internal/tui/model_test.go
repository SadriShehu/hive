package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
)

var now = time.Date(2026, 9, 28, 20, 0, 0, 0, time.UTC)

func ago(d time.Duration) int64 { return now.Add(-d).UnixMilli() }

// fakeOps records what the TUI asked for.
type fakeOps struct {
	sessions []store.Session
	focused  []string
	launched []tracker.LaunchOptions
	resumed  []string
	sent     []string
	stopped  []string
	trash    []store.Session // what is in the trash
	trashed  []string
	restored []string
	purged   []string
	copied   []string
}

func (f *fakeOps) Refresh() ([]store.Session, error) { return f.sessions, nil }
func (f *fakeOps) Sync(context.Context) error        { return nil }
func (f *fakeOps) Tail(s store.Session, n int) ([]agent.Line, error) {
	return []agent.Line{{Role: "user", Text: "prompt of " + s.ID}, {Role: "tool", Text: "Bash: go test"},
		{Role: "tool", Text: "bash: python3 - <<'EOF'\nimport re"}, {Role: "assistant", Text: "done"}}, nil
}
func (f *fakeOps) Capture(pane string) (string, error) { return "screen of " + pane + "\n\n", nil }
func (f *fakeOps) Focus(pane string) error             { f.focused = append(f.focused, pane); return nil }
func (f *fakeOps) Launch(o tracker.LaunchOptions) (tracker.Launched, error) {
	f.launched = append(f.launched, o)
	return tracker.Launched{Pane: "%9"}, nil
}
func (f *fakeOps) CanResume(s store.Session) error {
	if s.Live() {
		return errors.New("it is already running")
	}
	return nil
}
func (f *fakeOps) Resume(s store.Session, prompt string) (tracker.Launched, error) {
	f.resumed = append(f.resumed, s.ID+"|"+prompt)
	return tracker.Launched{Pane: "%8"}, nil
}
func (f *fakeOps) Send(s store.Session, text string) (string, error) {
	f.sent = append(f.sent, s.ID+"|"+text)
	return "sent", nil
}
func (f *fakeOps) Stop(s store.Session) error { f.stopped = append(f.stopped, s.ID); return nil }
func (f *fakeOps) Trash(s store.Session) ([]store.Session, error) {
	f.trashed = append(f.trashed, s.ID)
	return append([]store.Session{s}, under(f.sessions, s.ID)...), nil
}
func (f *fakeOps) Trashed() ([]store.Session, error) { return f.trash, nil }
func (f *fakeOps) Restore(s store.Session) ([]store.Session, error) {
	f.restored = append(f.restored, s.ID)
	return append([]store.Session{s}, under(f.trash, s.ID)...), nil
}
func (f *fakeOps) Purge(s store.Session) (tracker.Purged, error) {
	f.purged = append(f.purged, s.ID)
	return tracker.Purged{Sessions: append([]store.Session{s}, under(f.trash, s.ID)...)}, nil
}
func (f *fakeOps) Copy(text string) error { f.copied = append(f.copied, text); return nil }
func (f *fakeOps) Tools() []string        { return []string{"claude", "opencode"} }

// sample is a Claude session in a pane that spawned a headless opencode run
// (which has its own subagent) and a Claude run that finished; plus an old
// session from last week.
func sample() []store.Session {
	return []store.Session{
		{ID: "claude:A", Tool: "claude", NativeID: "A", Title: "Admin phase 2", Cwd: "/src/app", Kind: "interactive",
			Status: "working", PID: 100, Pane: "%1", CreatedAt: ago(2 * time.Hour), UpdatedAt: ago(time.Minute)},
		{ID: "opencode:B", Tool: "opencode", NativeID: "B", ParentID: "claude:A", Title: "impl-backend", Cwd: "/src/app/backend",
			Kind: "headless", Status: "working", PID: 201, CreatedAt: ago(time.Hour), UpdatedAt: ago(time.Minute)},
		{ID: "opencode:G", Tool: "opencode", NativeID: "G", ParentID: "opencode:B", Title: "explore handlers",
			Kind: "internal", Status: "working", CreatedAt: ago(50 * time.Minute), UpdatedAt: ago(time.Minute)},
		{ID: "claude:C", Tool: "claude", NativeID: "C", ParentID: "claude:A", Title: "lint fix", Cwd: "/src/app",
			Kind: "headless", Status: "exited", CreatedAt: ago(40 * time.Minute), UpdatedAt: ago(30 * time.Minute)},
		{ID: "claude:OLD", Tool: "claude", NativeID: "OLD", Title: "last week's refactor", Cwd: "/src/old",
			Kind: "interactive", Status: "exited", CreatedAt: ago(7 * 24 * time.Hour), UpdatedAt: ago(7 * 24 * time.Hour)},
		// A spare session an agent pre-warmed and never used.
		{ID: "claude:SPARE", Tool: "claude", NativeID: "SPARE", Cwd: "/src/app", Kind: "interactive",
			Status: "exited", Source: "hook", CreatedAt: ago(10 * time.Minute), UpdatedAt: ago(10 * time.Minute)},
	}
}

func setup(t *testing.T, popup bool) (Model, *fakeOps) {
	t.Helper()
	ops := &fakeOps{sessions: sample()}
	m := New(ops, popup)
	m.now = func() time.Time { return now }
	m = step(t, m, tea.WindowSizeMsg{Width: 160, Height: 30})
	m = step(t, m, refreshedMsg{sessions: ops.sessions})
	return m, ops
}

// step feeds msg to the model, runs the commands it returns (timers aside)
// and feeds their results back, the way Bubble Tea would. A quit shows as
// flash "QUIT".
func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	queue := []tea.Msg{msg}
	for n := 0; len(queue) > 0 && n < 50; n++ {
		msg, queue = queue[0], queue[1:]
		if _, isQuit := msg.(tea.QuitMsg); isQuit {
			m.flash = "QUIT"
			continue
		}
		next, cmd := m.Update(msg)
		m = next.(Model)
		queue = append(queue, runCmd(cmd)...)
	}
	return m
}

func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(50 * time.Millisecond):
		return nil // a timer (tick, cursor blink): not part of the test
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, runCmd(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

func press(t *testing.T, m Model, ks ...string) Model {
	t.Helper()
	for _, k := range ks {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "up":
			msg = tea.KeyMsg{Type: tea.KeyUp}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m = step(t, m, msg)
	}
	return m
}

func rowIDs(m Model) []string {
	var ids []string
	for _, r := range m.rows {
		ids = append(ids, r.node.ID)
	}
	return ids
}

func TestTreeShowsRecentSessionsWithTheirChildren(t *testing.T) {
	m, _ := setup(t, false)
	want := "claude:A opencode:B opencode:G claude:C"
	if got := strings.Join(rowIDs(m), " "); got != want {
		t.Fatalf("rows = %s, want %s", got, want)
	}
	view := ansi.Strip(m.View())
	for _, s := range []string{"├─ ", "│  └─ ", "└─ ", "Admin phase 2", "impl-backend", "run", "sub", "3 live"} {
		if !strings.Contains(view, s) {
			t.Errorf("view lacks %q:\n%s", s, view)
		}
	}
	if strings.Contains(view, "last week") {
		t.Error("old session shown without 'a'")
	}
	m = press(t, m, "a")
	if len(m.rows) != 5 {
		t.Errorf("with all history: %d rows, want 5", len(m.rows))
	}
}

func TestSelectionSurvivesRefresh(t *testing.T) {
	m, ops := setup(t, false)
	m = press(t, m, "j", "j", "j") // claude:C
	ops.sessions = append([]store.Session{{ID: "claude:NEW", Tool: "claude", NativeID: "NEW", Title: "brand new",
		Status: "idle", PID: 300, Pane: "%5", CreatedAt: ago(time.Second), UpdatedAt: ago(0)}}, ops.sessions...)
	m = step(t, m, refreshedMsg{sessions: ops.sessions})
	if s, _ := m.selected(); s.ID != "claude:C" {
		t.Fatalf("selection moved to %s", s.ID)
	}
}

func TestFilterFoldAndSubagents(t *testing.T) {
	m, _ := setup(t, false)
	m = press(t, m, "/", "e", "x", "p", "l", "o", "r", "e", "enter")
	if got := strings.Join(rowIDs(m), " "); got != "claude:A opencode:B opencode:G" {
		t.Fatalf("filtered rows = %s: want the match and its ancestors", got)
	}
	m = press(t, m, "esc")
	if len(m.rows) != 4 {
		t.Fatalf("esc left %d rows", len(m.rows))
	}

	m = press(t, m, "space") // fold claude:A
	if got := strings.Join(rowIDs(m), " "); got != "claude:A" || !strings.Contains(ansi.Strip(m.View()), "(+3)") {
		t.Fatalf("folded rows = %s", got)
	}
	m = press(t, m, "l", "i") // unfold, hide subagents
	if got := strings.Join(rowIDs(m), " "); got != "claude:A opencode:B claude:C" {
		t.Fatalf("rows without subagents = %s", got)
	}
}

func TestJump(t *testing.T) {
	m, ops := setup(t, false)
	m = press(t, m, "enter")           // claude:A has a pane
	m = press(t, m, "j", "j", "enter") // opencode:G lives inside B, which has no pane
	m = press(t, m, "k", "enter")      // opencode:B is a headless run
	m = press(t, m, "j", "j", "enter") // claude:C finished: reopen it
	if strings.Join(ops.focused, ",") != "%1" {
		t.Errorf("focused = %v, want only %%1", ops.focused)
	}
	if strings.Join(ops.resumed, ",") != "claude:C|" {
		t.Errorf("resumed = %v", ops.resumed)
	}
	if m.flash == "QUIT" {
		t.Error("dashboard quit after a jump")
	}
}

func TestPopupClosesAfterJumping(t *testing.T) {
	m, ops := setup(t, true)
	m = press(t, m, "enter")
	if m.flash != "QUIT" || len(ops.focused) != 1 {
		t.Fatalf("popup after jump: flash=%q focused=%v", m.flash, ops.focused)
	}
}

func TestSendNewChildAndStop(t *testing.T) {
	m, ops := setup(t, false)
	m = press(t, m, "s", "h", "i", "enter")
	if strings.Join(ops.sent, ",") != "claude:A|hi" {
		t.Errorf("sent = %v", ops.sent)
	}

	m = press(t, m, "c")
	if m.mode != modeNew || m.form.folder.Value() != "/src/app" || m.form.tools[m.form.tool] != "claude" {
		t.Fatalf("child form: mode=%v folder=%q tool=%v", m.mode, m.form.folder.Value(), m.form.tools[m.form.tool])
	}
	m = press(t, m, "tab", "tab") // folder → prompt → tool
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRight})
	m = press(t, m, "tab", "tab", "g", "o", "enter")
	if len(ops.launched) != 1 {
		t.Fatalf("launched = %+v", ops.launched)
	}
	if l := ops.launched[0]; l.Tool != "opencode" || l.ParentID != "claude:A" || l.Cwd != "/src/app" || l.Prompt != "go" {
		t.Errorf("launch = %+v", l)
	}

	m = press(t, m, "x", "n")
	m = press(t, m, "x", "y")
	if strings.Join(ops.stopped, ",") != "claude:A" {
		t.Errorf("stopped = %v, want claude:A once", ops.stopped)
	}
	m = press(t, m, "y")
	if strings.Join(ops.copied, ",") != "claude:A" {
		t.Errorf("copied = %v", ops.copied)
	}
}

func TestNewFormWrapsLongPrompt(t *testing.T) {
	m, ops := setup(t, false)
	m = press(t, m, "n", "tab")
	long := strings.Repeat("make the admin page load faster ", 6) + "and add tests"
	m = press(t, m, long)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "and add tests") {
		t.Fatalf("end of the prompt not shown:\n%s", view)
	}
	rows := 0
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "admin page") {
			rows++
		}
	}
	if rows < 3 {
		t.Errorf("long prompt drawn on %d rows, want it wrapped:\n%s", rows, view)
	}
	m = press(t, m, "enter")
	if len(ops.launched) != 1 || ops.launched[0].Prompt != long {
		t.Errorf("launched = %+v, want the whole prompt", ops.launched)
	}
	if m.mode != modeNormal {
		t.Errorf("form still open after starting the agent")
	}
}

func TestSendInputWrapsLongMessage(t *testing.T) {
	m, ops := setup(t, false)
	full := m.bodyHeight()
	m = press(t, m, "s")
	long := strings.Repeat("please review the admin handlers for races ", 7) + "then report"
	m = press(t, m, long)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "then report") || !strings.Contains(view, "send to claude") {
		t.Fatalf("end of the message or its label not shown:\n%s", view)
	}
	if lines := strings.Split(m.View(), "\n"); len(lines) != 30 {
		t.Errorf("view has %d lines, want 30", len(lines))
	}
	if m.bodyHeight() >= full {
		t.Errorf("body height %d did not shrink below %d for the wrapped message", m.bodyHeight(), full)
	}
	m = press(t, m, "enter")
	if strings.Join(ops.sent, ",") != "claude:A|"+long {
		t.Errorf("sent = %v, want the whole message", ops.sent)
	}
	if m.mode != modeNormal || m.bodyHeight() != full {
		t.Errorf("after sending: mode=%v body height=%d, want normal and %d", m.mode, m.bodyHeight(), full)
	}
}

func TestDeleteMovesToTheTrashCountingEverythingUnder(t *testing.T) {
	ops := &fakeOps{sessions: append(sample(),
		store.Session{ID: "claude:C/s1", Tool: "claude", NativeID: "C/s1", ParentID: "claude:C", Title: "explore",
			Kind: "internal", Status: "exited", CreatedAt: ago(35 * time.Minute), UpdatedAt: ago(34 * time.Minute)})}
	m := New(ops, false)
	m.now = func() time.Time { return now }
	m = step(t, m, tea.WindowSizeMsg{Width: 160, Height: 30})
	m = step(t, m, refreshedMsg{sessions: ops.sessions})

	m = press(t, m, "d")
	if m.mode != modeNormal || !strings.Contains(m.flash, "still running") {
		t.Fatalf("delete on a live tree: mode=%v flash=%q", m.mode, m.flash)
	}
	// With subagents hidden, C shows no children, but its subagent goes too.
	m = press(t, m, "i", "j", "j", "d") // claude:C: finished
	if view := ansi.Strip(m.View()); m.mode != modeConfirm ||
		!strings.Contains(view, "move claude ‹lint fix› and the 1 session under it to the trash?") {
		t.Fatalf("delete on an ended session: mode=%v\n%s", m.mode, view)
	}
	m = press(t, m, "n")
	if len(ops.trashed) != 0 || m.mode != modeNormal {
		t.Fatalf("declined delete still ran: %v", ops.trashed)
	}
	m = press(t, m, "d", "y")
	if strings.Join(ops.trashed, ",") != "claude:C" {
		t.Fatalf("trashed = %v, want claude:C", ops.trashed)
	}
	if !strings.Contains(m.flash, "moved claude ‹lint fix› and the 1 session under it to the trash") {
		t.Errorf("flash = %q", m.flash)
	}
}

func TestTrashRestoresAndDeletesForGood(t *testing.T) {
	m, ops := setup(t, false)
	ops.trash = []store.Session{
		{ID: "claude:T", Tool: "claude", NativeID: "T", Title: "old spike", Status: "exited",
			CreatedAt: ago(3 * time.Hour), UpdatedAt: ago(2 * time.Hour), DeletedAt: ago(time.Hour)},
		{ID: "opencode:U", Tool: "opencode", NativeID: "U", ParentID: "claude:T", Kind: "headless", Status: "exited",
			CreatedAt: ago(3 * time.Hour), UpdatedAt: ago(2 * time.Hour), DeletedAt: ago(time.Hour)},
	}
	m = step(t, m, refreshedMsg{sessions: ops.sessions, trashed: ops.trash})
	if view := ansi.Strip(m.View()); !strings.Contains(view, "2 in the trash") {
		t.Fatalf("the header doesn't count the trash:\n%s", view)
	}
	m = press(t, m, "t")
	view := ansi.Strip(m.View())
	if !m.trash || !strings.Contains(view, "old spike") || strings.Contains(view, "Admin phase 2") ||
		!strings.Contains(view, "in the trash since 1h ago") {
		t.Fatalf("the trash view:\n%s", view)
	}
	m = press(t, m, "enter")
	if len(ops.focused)+len(ops.resumed) != 0 || !strings.Contains(m.flash, "r restores") {
		t.Fatalf("enter in the trash: focused=%v resumed=%v flash=%q", ops.focused, ops.resumed, m.flash)
	}
	m = press(t, m, "r")
	if strings.Join(ops.restored, ",") != "claude:T" || !strings.Contains(m.flash, "restored claude ‹old spike› and the 1 session under it") {
		t.Fatalf("restore: %v, flash %q", ops.restored, m.flash)
	}
	m = press(t, m, "d")
	if view := ansi.Strip(m.View()); !strings.Contains(view, "delete claude ‹old spike› and the 1 session under it for good") {
		t.Fatalf("the purge question:\n%s", view)
	}
	m = press(t, m, "y")
	if strings.Join(ops.purged, ",") != "claude:T" || !strings.Contains(m.flash, "for good") {
		t.Fatalf("purge: %v, flash %q", ops.purged, m.flash)
	}
	m = press(t, m, "t")
	if m.trash || !strings.Contains(ansi.Strip(m.View()), "Admin phase 2") {
		t.Fatal("t didn't go back to the tree")
	}
}

func TestPreviewShowsPaneOrTranscript(t *testing.T) {
	m, _ := setup(t, false)
	if view := ansi.Strip(m.View()); !strings.Contains(view, "screen of %1") || !strings.Contains(view, "pane %1 · ↵ jumps there") {
		t.Fatalf("pane preview missing:\n%s", view)
	}
	m = press(t, m, "j", "j", "j") // claude:C: finished, from its transcript
	view := ansi.Strip(m.View())
	for _, s := range []string{"› prompt of claude:C", "⚙ Bash: go test", "done", "from claude ‹Admin phase 2›", "↵ reopens it"} {
		if !strings.Contains(view, s) {
			t.Errorf("transcript preview lacks %q:\n%s", s, view)
		}
	}
}

func TestViewFitsAnySize(t *testing.T) {
	for _, size := range [][2]int{{30, 8}, {80, 20}, {99, 24}, {100, 24}, {160, 40}, {240, 70}} {
		m, _ := setup(t, false)
		m = step(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, mode := range []string{"", "?", "n", "s"} {
			mm := m
			if mode != "" {
				mm = press(t, m, mode)
			}
			if mode == "n" {
				mm = press(t, mm, "tab", strings.Repeat("wrap me ", 40))
			}
			if mode == "s" {
				mm = press(t, mm, strings.Repeat("wrap me ", 40))
			}
			lines := strings.Split(mm.View(), "\n")
			if len(lines) != size[1] {
				t.Errorf("%dx%d mode %q: %d lines", size[0], size[1], mode, len(lines))
			}
			for i, l := range lines {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Errorf("%dx%d mode %q: line %d is %d wide: %q", size[0], size[1], mode, i, w, ansi.Strip(l))
				}
			}
		}
	}
}
