package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/store"
)

func TestADoneTeamIsFoldedDimmedAndTakesNoMessages(t *testing.T) {
	m, ops := setupTeam(t)
	ops.snap.Teams[0].Status = store.TeamDone
	m = step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})
	if got := strings.Join(rowIDs(m), " "); got != "team:t1" {
		t.Fatalf("rows = %s, want the done team folded", got)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"✓ done · 3 members", "the goal is met", "start with the schema"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	m = press(t, m, "s")
	if m.mode == modeSend || !strings.Contains(m.flash, "is done") {
		t.Errorf("s on a done team: mode %v, flash %q", m.mode, m.flash)
	}

	// Unfolded, it stays unfolded on the next refresh.
	m = press(t, m, "right")
	m = step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})
	if got := strings.Join(rowIDs(m), " "); got != "team:t1 claude:L claude:B codex:F" {
		t.Errorf("rows = %s, want the team unfolded with its members", got)
	}
}
