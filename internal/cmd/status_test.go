package cmd

import (
	"testing"

	"github.com/sadrishehu/hive/internal/store"
)

func TestStatusText(t *testing.T) {
	sessions := []store.Session{
		{ID: "claude:A", Kind: store.KindInteractive, Status: store.StatusAttention, PID: 1},
		{ID: "claude:A/sub", ParentID: "claude:A", Kind: store.KindInternal, Status: store.StatusWorking},
		{ID: "opencode:B", Kind: store.KindHeadless, Status: store.StatusWorking, PID: 2},
		{ID: "opencode:B/sub", ParentID: "opencode:B", Kind: store.KindInternal, Status: store.StatusAttention},
		{ID: "codex:C", Kind: store.KindInteractive, Status: store.StatusIdle, PID: 3},
		{ID: "codex:D", Kind: store.KindInteractive, Status: store.StatusUnknown, PID: 4, Source: "scan"},
		{ID: "claude:E", Kind: store.KindInteractive, Status: store.StatusWorking}, // ended
	}
	if got := statusText(sessions, nil); got != "◆2 ●1 ◉1" {
		t.Errorf("plain = %q, want ◆2 ●1 ◉1: a subagent counts only when it needs you", got)
	}
	want := "#[fg=red,bold]◆2#[fg=default,nobold] #[fg=yellow]●1#[fg=default] #[fg=green]◉1#[fg=default]"
	if got := statusText(sessions, tmuxStyles); got != want {
		t.Errorf("tmux = %q\nwant   %q", got, want)
	}
	if got := statusText(sessions[2:3], nil); got != "●1" {
		t.Errorf("one working = %q: zero counts are left out", got)
	}
	if got := statusText(sessions[6:], tmuxStyles); got != "" {
		t.Errorf("nothing running = %q, want nothing at all", got)
	}
}
