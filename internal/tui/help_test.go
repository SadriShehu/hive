package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestHelpLinesFitEveryWidth(t *testing.T) {
	m, _ := setup(t, false)
	for _, w := range []int{60, 90, 120} {
		m = step(t, m, tea.WindowSizeMsg{Width: w, Height: 24})
		for _, line := range m.helpLines(w) {
			if got := ansi.StringWidth(line); got > w {
				t.Errorf("%d-col help line is %d wide:\n%s", w, got, ansi.Strip(line))
			}
		}
		m = press(t, m, "?")
		for _, line := range strings.Split(m.View(), "\n") {
			if got := ansi.StringWidth(line); got > w {
				t.Errorf("%d-col help view line is %d wide:\n%s", w, got, ansi.Strip(line))
			}
		}
		m = press(t, m, "esc")
	}
}

// TestHelpKeepsEveryKeyAndFact lists what the flat help said before the
// sections: none of it may be lost.
func TestHelpKeepsEveryKeyAndFact(t *testing.T) {
	var all strings.Builder
	keys := map[string]bool{}
	for _, s := range helpSections() {
		for _, r := range s.rows {
			if r.key != "" {
				keys[r.key] = true
			}
			all.WriteString(r.key + " " + r.text + "\n")
		}
	}
	for _, key := range []string{
		"↵", "s", "n / c", "r", "x", "d", "t", "/", "a", "i", "←/→ space",
		"y", "T", "S", "tab", "u", "q", "e", "ctrl+g", "p / L", "m", "x / d",
		"type", "↑/↓", "pgup/pgdn", "j / k", "n", "b", "esc",
	} {
		if !keys[key] {
			t.Errorf("help lost the key %q", key)
		}
	}
	text := all.String()
	for _, fact := range []string{
		"jump to the session's pane; reopen it if it isn't running",
		"send a message: typed into its pane, or reopens it with the message",
		"tool or model auto picks for the prompt",
		"reopen a finished session in its tool's own TUI",
		"stop the session's process (asks first)",
		"move the session and everything under it to the trash (asks first)",
		"show the trash: r restores, d deletes for good, from its tool too (asks first)",
		"filter by title, folder or ID (searches all history); team:<name or ID> shows a team's subtree, bare team: every team",
		"show all history / only the last 24h",
		"hide / show subagents",
		"fold and unfold; ← on a leaf goes to its parent",
		"copy the session ID",
		"start a team: agents that work on one goal and report to a channel",
		"open the channel full screen",
		"edit the board",
		"read the whole goal and every member's part",
		"message everyone",
		"pause or resume",
		"take the lead or hand it back",
		"stop every member",
		"move the team to the trash",
		"hide hive's own posts",
		"write to everyone (@role for one) · ↵ sends · esc goes back",
		"move the caret once typing",
		"scroll the feed",
		"read the goal",
		"sync history now (it also runs every 30s)",
		"hide / show the preview",
		"show / hide what the session used: model, tokens, price, context, tools, skills",
		"quit (esc too, in the popup)",
		"● working  ◆ needs you  ◉ idle  ◌ running / untracked  ○ exited",
		"run: a headless run   sub: a tool's own subagent",
		"$ in the header: what the sessions in view cost · ~ an estimate from list prices · partial: totals come when the session ends",
	} {
		if !strings.Contains(text, fact) {
			t.Errorf("help lost the fact %q", fact)
		}
	}
}

func TestHelpOpensOnTheCurrentViewsKeysFirst(t *testing.T) {
	m, _ := setup(t, false)
	if got := m.helpOrders()[0].name; got != "Tree" {
		t.Errorf("help on the tree starts with %s, want Tree", got)
	}

	m, _ = setupTeam(t)
	m = press(t, m, "?")
	if got := m.helpOrders()[0].name; got != "Team" {
		t.Errorf("help on a team row starts with %s, want Team", got)
	}
	m = press(t, m, "esc")

	m = press(t, m, "j") // a member row
	m = press(t, m, "?")
	if got := m.helpOrders()[0].name; got != "Team" {
		t.Errorf("help on a member row starts with %s, want Team", got)
	}
	m = press(t, m, "esc")

	m = press(t, m, "t")
	m = press(t, m, "?")
	if got := m.helpOrders()[0].name; got != "Trash" {
		t.Errorf("help in the trash starts with %s, want Trash", got)
	}
	m = press(t, m, "esc")

	m, _ = setupTeam(t)
	m = press(t, m, "e") // the board
	m = press(t, m, "?")
	if m.mode != modeHelp {
		t.Fatalf("? in the board didn't open help: mode %v", m.mode)
	}
	if got := m.helpOrders()[0].name; got != "Board" {
		t.Errorf("help in the board starts with %s, want Board", got)
	}
	m = press(t, m, "esc")
	if m.mode != modeTasks {
		t.Errorf("esc from the board's help went to mode %v, want the board", m.mode)
	}
}

func TestHelpScrollsToTheLastLineAndStops(t *testing.T) {
	m, _ := setup(t, false)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = press(t, m, "?")
	if m.mode != modeHelp {
		t.Fatalf("? didn't open help: mode %v", m.mode)
	}
	maxScroll := m.helpMaxScroll(m.bodyHeight())
	if maxScroll == 0 {
		t.Fatal("the help fits, so there is nothing to scroll")
	}
	for range maxScroll + 5 {
		m = press(t, m, "j")
	}
	if m.helpScroll != maxScroll {
		t.Fatalf("scroll = %d after the end, want %d", m.helpScroll, maxScroll)
	}
	if m.mode != modeHelp {
		t.Fatalf("j closed the help: mode %v", m.mode)
	}
	if got := ansi.Strip(m.hintLine()); !strings.Contains(got, "lines") {
		t.Errorf("scrolled hint lacks the line count:\n%s", got)
	}

	m = press(t, m, "k")
	if m.helpScroll != maxScroll-1 {
		t.Errorf("k scroll = %d, want %d", m.helpScroll, maxScroll-1)
	}
	m = press(t, m, "G")
	if m.helpScroll != maxScroll {
		t.Errorf("G scroll = %d, want %d", m.helpScroll, maxScroll)
	}
	m = press(t, m, "esc")
	if m.mode != modeNormal {
		t.Errorf("esc left the help on mode %v", m.mode)
	}
}

func TestHelpHintSaysHowManyLines(t *testing.T) {
	m, _ := setup(t, false)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = press(t, m, "?")
	got := ansi.Strip(m.hintLine())
	if !strings.Contains(got, "↑/↓ scroll") || !strings.Contains(got, "esc close") {
		t.Errorf("help hint lacks its keys:\n%s", got)
	}
	total := len(m.helpLines(80))
	if !strings.Contains(got, " of "+strconv.Itoa(total)+" lines") {
		t.Errorf("help hint lacks the line count %d:\n%s", total, got)
	}
}
