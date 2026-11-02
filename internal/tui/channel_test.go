package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/store"
)

func TestChannelInputDropsWholeHintSegments(t *testing.T) {
	m, ops := setupTeam(t)
	ops.snap.Teams[0].Name = strings.Repeat("a", 20)
	m = step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = press(t, m, "enter")

	allowed := map[string]bool{
		"type a message · ↵ sends · esc goes back": true,
		"type a message · ↵ sends":                 true,
		"type a message":                           true,
		"":                                         true,
	}
	if got := m.channelInput.Placeholder; !allowed[got] {
		t.Errorf("placeholder = %q, want whole segments only", got)
	}
	hints := ansi.Strip(strings.Join(m.hintLines(), "\n"))
	if !strings.Contains(hints, "@role for one") {
		t.Errorf("a 20-char team name shouldn't drop the role hint:\n%s", hints)
	}
	for _, line := range m.hintLines() {
		if w := ansi.StringWidth(line); w > 80 {
			t.Errorf("the input line is %d wide:\n%s", w, ansi.Strip(line))
		}
	}
}

func TestChannelInputDropsTheRoleHintAndPlaceholderWhenVeryNarrow(t *testing.T) {
	m, ops := setupTeam(t)
	ops.snap.Teams[0].Name = strings.Repeat("b", 60)
	m = step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = press(t, m, "enter")

	if got := m.channelInput.Placeholder; got != "" {
		t.Errorf("placeholder = %q, want none when the prompt leaves no room", got)
	}
	hints := ansi.Strip(strings.Join(m.hintLines(), "\n"))
	if strings.Contains(hints, "@role for one") {
		t.Errorf("a 60-char team name kept the role hint:\n%s", hints)
	}
	for _, line := range m.hintLines() {
		if w := ansi.StringWidth(line); w > 80 {
			t.Errorf("the input line is %d wide:\n%s", w, ansi.Strip(line))
		}
	}
}

func TestChannelFeedWrapsUnderTheNameOnNarrowScreens(t *testing.T) {
	m, _ := setupTeam(t)
	team := m.snap.Teams[0]
	m.feed = feed{teamID: "t1", msgs: []store.Message{{
		ID: 9, TeamID: "t1", From: "lead", Body: strings.Repeat("word ", 40),
		Kind: store.MsgPost, At: ago(2 * time.Minute),
	}}}
	lines := m.feedLines(team, 80)
	if len(lines) < 2 {
		t.Fatalf("the body didn't wrap: %d lines", len(lines))
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w > 80 {
			t.Errorf("line %d is %d wide:\n%s", i, w, ansi.Strip(line))
		}
		if i == 0 {
			continue
		}
		plain := ansi.Strip(line)
		if lead := len(plain) - len(strings.TrimLeft(plain, " ")); lead != 6 {
			t.Errorf("continuation line %d starts at col %d, want 6:\n%s", i, lead, plain)
		}
	}
}

func TestChannelFeedWaitingSuffixFollowsTheIndent(t *testing.T) {
	m, _ := setupTeam(t)
	team := m.snap.Teams[0]
	m.feed = feed{
		teamID:  "t1",
		msgs:    []store.Message{{ID: 9, TeamID: "t1", From: "lead", To: "backend", Body: strings.Repeat("word ", 30), Kind: store.MsgSay, At: ago(2 * time.Minute)}},
		waiting: map[int64][]string{9: {"backend"}},
	}
	for _, tc := range []struct{ w, indent int }{{80, 6}, {120, 6 + ansi.StringWidth("lead → backend") + 1}} {
		lines := m.feedLines(team, tc.w)
		last := ansi.Strip(lines[len(lines)-1])
		if lead := len(last) - len(strings.TrimLeft(last, " ")); !strings.Contains(last, "waits for backend") || lead != tc.indent {
			t.Errorf("%d cols: suffix line starts at col %d, want %d:\n%s", tc.w, lead, tc.indent, last)
		}
	}
}

func TestChannelFeedKeepsTheWideLayoutFrom100Cols(t *testing.T) {
	m, _ := setupTeam(t)
	team := m.snap.Teams[0]
	m.feed = feed{teamID: "t1", msgs: []store.Message{{
		ID: 9, TeamID: "t1", From: "lead", Body: strings.Repeat("word ", 40),
		Kind: store.MsgPost, At: ago(2 * time.Minute),
	}}}
	lines := m.feedLines(team, 120)
	if len(lines) < 2 {
		t.Fatalf("the body didn't wrap: %d lines", len(lines))
	}
	want := 6 + ansi.StringWidth("lead") + 1
	stamp := time.UnixMilli(ago(2 * time.Minute)).Format("15:04")
	for i, line := range lines {
		if w := ansi.StringWidth(line); w > 120 {
			t.Errorf("line %d is %d wide:\n%s", i, w, ansi.Strip(line))
		}
		if i == 0 {
			if plain := ansi.Strip(line); !strings.HasPrefix(plain, stamp+" lead word") {
				t.Errorf("the wide first line changed:\n%s", plain)
			}
			continue
		}
		plain := ansi.Strip(line)
		if lead := len(plain) - len(strings.TrimLeft(plain, " ")); lead != want {
			t.Errorf("continuation line %d starts at col %d, want %d:\n%s", i, lead, want, plain)
		}
	}
}
