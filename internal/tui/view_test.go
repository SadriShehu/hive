package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestFitHintsDropsWholeHintsAndKeepsTheTail(t *testing.T) {
	hints := []string{
		keys("↵", "jump"),
		keys("s", "send"),
		keys("n", "new"),
		keys("c", "child"),
		keys("u", "usage"),
		keys("/", "filter"),
		keys("r", "reopen"),
		keys("x", "stop"),
		keys("d", "trash"),
	}
	tail := keys("?", "more")
	for _, w := range []int{40, 80, 120} {
		got := fitHints(hints, w, tail)
		want := tail
		for k := len(hints); k >= 0; k-- {
			line := strings.Join(hints[:k], "  ")
			if line != "" {
				line += "  "
			}
			line += tail
			if ansi.StringWidth(line) <= w {
				want = line
				break
			}
		}
		if got != want {
			t.Errorf("fitHints(%d) =\n%q\nwant\n%q", w, got, want)
		}
		if ansi.StringWidth(got) > w {
			t.Errorf("fitHints(%d) is %d wide:\n%s", w, ansi.StringWidth(got), ansi.Strip(got))
		}
		plain := ansi.Strip(got)
		if !strings.Contains(plain, "? more") {
			t.Errorf("fitHints(%d) dropped the tail:\n%s", w, plain)
		}
		for _, hint := range hints {
			p := ansi.Strip(hint)
			if strings.Contains(plain, p) {
				continue
			}
			label := strings.Fields(p)[1]
			if strings.Contains(plain, label) {
				t.Errorf("fitHints(%d) cut %q mid-hint:\n%s", w, p, plain)
			}
		}
	}
}

func TestTeamAndMemberHintsNameTheBoard(t *testing.T) {
	m, _ := setupTeam(t)
	check := func(w int, view string) {
		t.Helper()
		plain := ansi.Strip(view)
		if !strings.Contains(plain, "e board") {
			t.Errorf("%d-col hint lacks e board:\n%s", w, plain)
		}
		if !strings.Contains(plain, "? more") {
			t.Errorf("%d-col hint lacks the tail:\n%s", w, plain)
		}
		for _, line := range strings.Split(view, "\n") {
			if width := ansi.StringWidth(line); width > w {
				t.Errorf("%d-col hint line is %d wide:\n%s", w, width, ansi.Strip(line))
			}
		}
	}
	for _, w := range []int{60, 80, 120} {
		m = step(t, m, tea.WindowSizeMsg{Width: w, Height: 24})
		check(w, strings.Join(m.hintLines(), "\n"))
	}

	m = press(t, m, "j") // the lead, a member row
	for _, w := range []int{60, 80, 120} {
		m = step(t, m, tea.WindowSizeMsg{Width: w, Height: 24})
		check(w, strings.Join(m.hintLines(), "\n"))
	}
}

func TestSessionHintsAreMostUsedFirst(t *testing.T) {
	m, _ := setup(t, false)
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})
	got := ansi.Strip(strings.Join(m.hintLines(), "\n"))
	want := "↵ jump  s send  n new  c child  u usage  / filter  r reopen  x stop  d trash  ? more"
	if got != want {
		t.Fatalf("session hint =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "q quit") {
		t.Errorf("session hint still advertises q quit:\n%s", got)
	}
}
