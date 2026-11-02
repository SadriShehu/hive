package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/store"
)

func TestATeamRowMarksTheNameOrGoalThatMatched(t *testing.T) {
	m, _ := setupTeam(t)
	teamLine := func() string {
		for _, r := range m.rows {
			if r.node.Team != nil {
				return m.rowLine(r, 120, false)
			}
		}
		return ""
	}

	m = press(t, m, "/", "a", "p", "i", "enter")
	line := teamLine()
	if !strings.Contains(line, sAccent.Render("api")) {
		t.Errorf("a team name match isn't marked in accent:\n%s", line)
	}
	if len(m.rows) != 4 {
		t.Errorf("a matching team kept %d rows, want the team and its 3 members", len(m.rows))
	}

	m = press(t, m, "esc", "/", "a", "d", "m", "i", "n", "enter")
	line = teamLine()
	if !strings.Contains(line, sAccent.Render("admin")) {
		t.Errorf("a team goal match isn't marked in accent:\n%s", line)
	}
}

func TestMatchesSaysWhichFieldMatched(t *testing.T) {
	s := sample()[0] // Admin phase 2, /src/app, claude:A
	for _, tc := range []struct{ query, want string }{
		{"admin", "title"},
		{"src/app", "folder"},
		{"claude:a", "id"},
		{"xyzzy", ""},
	} {
		if got := matches(s, tc.query); got != tc.want {
			t.Errorf("matches(%q) = %q, want %q", tc.query, got, tc.want)
		}
	}
	team := teamSnapshot().Teams[0] // api-v2, add the admin API
	for _, tc := range []struct{ query, want string }{
		{"api", "name"},
		{"admin", "goal"},
		{"xyzzy", ""},
	} {
		if got := matchesTeam(team, tc.query); got != tc.want {
			t.Errorf("matchesTeam(%q) = %q, want %q", tc.query, got, tc.want)
		}
	}
}

func TestFilteredRowsSayWhyTheyMatched(t *testing.T) {
	m, _ := setup(t, false)

	// A title match marks the matched part and adds no tag.
	m = press(t, m, "/", "a", "d", "m", "i", "n", "enter")
	var line string
	for i, r := range m.rows {
		if r.node.ID == "claude:A" {
			line = m.rowLine(r, 100, i == m.cursor)
		}
	}
	if line == "" {
		t.Fatal("the title match isn't in the tree")
	}
	if !strings.Contains(line, sAccent.Render("Admin")) {
		t.Errorf("a title match isn't marked in accent:\n%s", line)
	}
	if strings.Contains(ansi.Strip(line), " folder") || strings.Contains(ansi.Strip(line), " id") {
		t.Errorf("a title match also got a tag:\n%s", ansi.Strip(line))
	}

	// A folder-only match adds its tag.
	m = press(t, m, "esc", "/", "s", "r", "c", "/", "a", "p", "p", "enter")
	line = ""
	for i, r := range m.rows {
		if r.node.ID == "claude:A" {
			line = m.rowLine(r, 100, i == m.cursor)
		}
	}
	if line == "" {
		t.Fatal("the folder match isn't in the tree")
	}
	if !strings.Contains(ansi.Strip(line), "folder") {
		t.Errorf("a folder-only match has no folder tag:\n%s", ansi.Strip(line))
	}
	if !strings.Contains(line, sDim.Render(" folder")) {
		t.Errorf("the folder tag isn't dim:\n%q", line)
	}
}

func TestNoFilterMatchNamesTheQuery(t *testing.T) {
	m, _ := setup(t, false)
	m = press(t, m, "/", "x", "y", "z", "z", "y", "enter")
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "nothing matches “xyzzy” in titles, folders or IDs (all history) · esc clears") {
		t.Errorf("the empty filter result doesn't name the query:\n%s", view)
	}
}

func TestHeaderNamesTheFilterAndSearchesAllHistory(t *testing.T) {
	m, _ := setup(t, false)
	m = press(t, m, "/", "r", "e", "v", "i", "e", "w", "enter")
	if head := ansi.Strip(m.headerLine()); !strings.Contains(head, "matching “review” · all history") {
		t.Errorf("the header doesn't say the filter searched all history:\n%s", head)
	}
}

func TestHeaderWarnsAboutToolsThatArentConnected(t *testing.T) {
	m, _ := setup(t, false)
	m = step(t, m, notConnectedMsg{missing: []string{"claude", "codex"}})
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 24})
	head := ansi.Strip(m.headerLine())
	want := "⚠ not connected: claude, codex · run hive install"
	if !strings.Contains(head, want) {
		t.Fatalf("the wide header lacks the warning:\n%s", head)
	}

	m = step(t, m, tea.WindowSizeMsg{Width: 60, Height: 24})
	head = ansi.Strip(m.headerLine())
	if w := ansi.StringWidth(head); w > 60 {
		t.Fatalf("the narrow header is %d wide:\n%s", w, head)
	}
	if strings.Contains(head, "not connected") && !strings.Contains(head, want) {
		t.Errorf("the narrow header cut the warning mid-word:\n%s", head)
	}
}

func TestAnUntrackedRowsPreviewSaysHowToConnectIt(t *testing.T) {
	scan := store.Session{ID: "claude:S", Tool: "claude", NativeID: "S", Cwd: "/src/app",
		Status: store.StatusUnknown, Source: "scan", PID: 99, CreatedAt: ago(5 * time.Minute), UpdatedAt: ago(time.Minute)}
	m, _ := setupWith(t, false, []store.Session{scan})
	m.preview = preview{id: scan.ID, err: "no transcript recorded for this session"}
	view := ansi.Strip(strings.Join(m.previewLines(100, 20), "\n"))
	if !strings.Contains(view, "hive can't see what this does until its tool reports: hive install claude, then restart it") {
		t.Errorf("an untracked row's preview doesn't say how to connect it:\n%s", view)
	}
}
