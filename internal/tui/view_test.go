package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	want := "↵ jump  s send  n new  c child  u usage  / filter  r reopen  x stop  d trash  t show trash  a all/24h  ? more"
	if got != want {
		t.Fatalf("session hint =\n%s\nwant\n%s", got, want)
	}
	m = step(t, m, tea.WindowSizeMsg{Width: 200, Height: 24})
	if wide := ansi.Strip(strings.Join(m.hintLines(), "\n")); !strings.HasSuffix(wide, "i subagents  ? more") {
		t.Errorf("a wide screen shows every session key:\n%s", wide)
	}
	if strings.Contains(got, "q quit") {
		t.Errorf("session hint still advertises q quit:\n%s", got)
	}
}

func TestChoicesKeepTheSelectedOneInView(t *testing.T) {
	items := make([]string, 20)
	for i := range items {
		items[i] = fmt.Sprintf("model-%02d", i)
	}
	style := func(string) lipgloss.Style { return sAccent }
	for _, w := range []int{30, 40, 80} {
		for selected := range items {
			got := choices(items, selected, w, style)
			plain := ansi.Strip(got)
			if !strings.Contains(plain, "‹ "+items[selected]+" ›") {
				t.Fatalf("choices(%d, %d) hides the selected item:\n%s", w, selected, plain)
			}
			if width := ansi.StringWidth(got); width > w {
				t.Fatalf("choices(%d, %d) is %d wide:\n%s", w, selected, width, plain)
			}
			if w == 30 {
				if !strings.Contains(plain, fmt.Sprintf("%d/20", selected+1)) {
					t.Errorf("choices(%d, %d) lacks its position:\n%s", w, selected, plain)
				}
				if !strings.Contains(plain, "…") {
					t.Errorf("choices(%d, %d) hides items with no ellipsis:\n%s", w, selected, plain)
				}
			}
		}
	}
}

func TestChoicesLookTheSameWhenEverythingFits(t *testing.T) {
	items := []string{"claude", "opencode", "codex"}
	got := choices(items, 1, 80, func(string) lipgloss.Style { return sAccent })
	want := sDim.Render("claude") + "  " + sAccent.Bold(true).Render("‹ opencode ›") + "  " + sDim.Render("codex")
	if got != want {
		t.Fatalf("choices =\n%q\nwant\n%q", got, want)
	}
}

func TestNarrowHeaderSaysWhyThePreviewIsHidden(t *testing.T) {
	m, _ := setup(t, false)
	m = step(t, m, tea.WindowSizeMsg{Width: 90, Height: 24})
	head := ansi.Strip(m.headerLine())
	if !strings.Contains(head, "preview needs 100 cols") {
		t.Fatalf("narrow header lacks the preview note:\n%s", head)
	}
	if strings.Contains(head, "$") {
		t.Errorf("narrow header kept the cost beside the preview note:\n%s", head)
	}

	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})
	head = ansi.Strip(m.headerLine())
	if strings.Contains(head, "preview needs 100 cols") {
		t.Errorf("wide header still says the preview is hidden:\n%s", head)
	}

	m = press(t, m, "tab") // hide the preview yourself
	m = step(t, m, tea.WindowSizeMsg{Width: 90, Height: 24})
	if head := ansi.Strip(m.headerLine()); strings.Contains(head, "preview needs 100 cols") {
		t.Errorf("hidden preview still claims it needs cols:\n%s", head)
	}
}
