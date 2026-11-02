package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/gitx"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
)

func sampleChanges() tracker.ChangeReport {
	return tracker.ChangeReport{
		Dir:  "/src/app",
		Repo: "/src/app",
		Files: []gitx.FileChange{
			{Path: "internal/tui/team.go", Status: "M", Added: 96, Removed: 12},
			{Path: "internal/tui/changes.go", Status: "A", Added: 8},
		},
		Uncommitted: 1,
		Diff:        "diff --git a/x b/x\n@@ -1 +1 @@\n-old line\n+new line\n",
	}
}

// D shows the changes and u the usage; the last one pressed wins.
func TestDAndUAreExclusive(t *testing.T) {
	m, _ := setup(t, false)
	m = press(t, m, "u")
	if !m.showUsage || m.showChanges {
		t.Fatalf("after u: usage %v changes %v", m.showUsage, m.showChanges)
	}
	m = press(t, m, "D")
	if !m.showChanges || m.showUsage {
		t.Fatalf("after D: usage %v changes %v", m.showUsage, m.showChanges)
	}
	if m.changes.id == "" {
		t.Error("D didn't ask for the selected session's changes")
	}
	m = press(t, m, "u")
	if !m.showUsage || m.showChanges {
		t.Fatalf("after u again: usage %v changes %v", m.showUsage, m.showChanges)
	}
}

// D on a team row says to pick a member, rather than showing a team's
// stand-in session.
func TestDOnATeamRowFlashes(t *testing.T) {
	m, _ := setupTeam(t)
	m = press(t, m, "D")
	if m.showChanges {
		t.Error("D turned on changes for a team row")
	}
	if !strings.Contains(m.flash, "pick a member") {
		t.Errorf("flash = %q, want the pick-a-member hint", m.flash)
	}
}

// A member's changes are labeled with its branch and the team's base.
func TestChangesLabelForAWorktreeMember(t *testing.T) {
	m, _ := setupTeam(t)
	m = press(t, m, "j", "j") // the team's backend member
	s, ok := m.selected()
	if !ok || m.selectedNode().Role != "backend" {
		t.Fatalf("selected = %v, %v; want backend", s.ID, ok)
	}
	rep := sampleChanges()
	rep.Worktree, rep.Branch, rep.Base, rep.Ahead = true, "hive/api-v2/backend", "feat/wave-2", 1
	m.changes = changesFetch{id: changesID(s, "t1", "backend"), got: true, report: rep}
	lines := m.changesLines(s, "backend", 100, 20)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{"changes on hive/api-v2/backend vs feat/wave-2", "D back",
		"2 files · +104 −12 · 1 uncommitted · 1 commit ahead", "M internal/tui/team.go", "+96", "−12"} {
		if !strings.Contains(plain, want) {
			t.Errorf("changes lack %q:\n%s", want, plain)
		}
	}
	raw := strings.Join(lines, "\n")
	if !strings.Contains(raw, sOK.Render("+new line")) || !strings.Contains(raw, sError.Render("-old line")) {
		t.Errorf("the diff isn't coloured:\n%s", raw)
	}
	if !strings.Contains(raw, sDim.Render("@@ -1 +1 @@")) {
		t.Errorf("the hunk header isn't dim:\n%s", raw)
	}
}

// A plain folder says what it is, and when live sessions share it.
func TestChangesLabelForASharedFolder(t *testing.T) {
	sessions := sample()
	sessions = append(sessions,
		store.Session{ID: "claude:X", Tool: "claude", Cwd: "/src/app", Status: "idle", PID: 300, CreatedAt: ago(time.Minute)},
		store.Session{ID: "claude:Y", Tool: "claude", Cwd: "/src/app", Status: "working", PID: 301, CreatedAt: ago(time.Minute)})
	m, _ := setupWith(t, false, sessions)
	s, _ := m.selected()
	rep := sampleChanges()
	m.changes = changesFetch{id: changesID(s, "", ""), got: true, report: rep}
	plain := ansi.Strip(strings.Join(m.changesLines(s, "", 100, 20), "\n"))
	if !strings.Contains(plain, "changes in this folder") {
		t.Errorf("plain folder label missing:\n%s", plain)
	}
	if !strings.Contains(plain, "shared with 2 live sessions, so not only Admin phase 2's") {
		t.Errorf("shared-folder note missing:\n%s", plain)
	}
}

// A folder that isn't a repository says so, and the preview is cut to fit.
func TestChangesNotARepoAndWidths(t *testing.T) {
	m, _ := setup(t, false)
	s, _ := m.selected()
	m.changes = changesFetch{id: changesID(s, "", ""), got: true, report: tracker.ChangeReport{Dir: "/src/app", NotRepo: true}}
	if plain := ansi.Strip(strings.Join(m.changesLines(s, "", 80, 10), "\n")); !strings.Contains(plain, "not a git repository: /src/app") {
		t.Errorf("not-a-repo line = %q", plain)
	}
	for _, w := range []int{60, 120} {
		m.changes = changesFetch{id: changesID(s, "", ""), got: true, report: sampleChanges()}
		lines := m.changesLines(s, "", w, 20)
		for _, line := range lines {
			if got := ansi.StringWidth(line); got > w {
				t.Errorf("%d wide: a line is %d: %q", w, got, ansi.Strip(line))
			}
		}
		if !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "D back") {
			t.Errorf("%d wide lost D back", w)
		}
	}
}

// v opens the full diff in a popup, with the binary and ID quoted, and only
// while the changes preview is on.
func TestVOpensTheFullDiffOnlyWithChanges(t *testing.T) {
	m, ops := setup(t, false)
	m = press(t, m, "v")
	if len(ops.popups) != 0 {
		t.Fatalf("v opened a popup without the changes preview: %v", ops.popups)
	}
	m = press(t, m, "D")
	m = press(t, m, "v")
	if len(ops.popups) != 1 {
		t.Fatalf("popups = %v, want one full diff", ops.popups)
	}
	s, _ := m.selected()
	if !strings.Contains(ops.popups[0], " diff ") || !strings.Contains(ops.popups[0], s.ID) {
		t.Errorf("popup command = %q, want the hive diff for %s", ops.popups[0], s.ID)
	}
}

// The changes preview is read again after five seconds, not every tick.
func TestChangesAreFetchedAtMostEveryFiveSeconds(t *testing.T) {
	m, _ := setup(t, false)
	m.showChanges = true
	m.changes = changesFetch{}
	if cmd := m.changesCmd(); cmd == nil {
		t.Fatal("no changes fetch with the preview on")
	}
	if cmd := m.changesCmd(); cmd != nil {
		t.Error("a second fetch started before five seconds passed")
	}
	m.now = func() time.Time { return now.Add(6 * time.Second) }
	if cmd := m.changesCmd(); cmd == nil {
		t.Error("no fetch after five seconds")
	}
}

// Step a key and show the preview still fits the body.
func TestChangesPreviewFitsTheBody(t *testing.T) {
	m, _ := setup(t, false)
	m = step(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = press(t, m, "D")
	if got := len(strings.Split(ansi.Strip(m.View()), "\n")); got != m.height {
		t.Errorf("view has %d lines, want %d", got, m.height)
	}
}
