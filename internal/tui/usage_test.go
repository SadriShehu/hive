package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/store"
)

func TestJoinFitDropsTrailingParts(t *testing.T) {
	if got := ansi.Strip(joinFit(20, "aaaa", "bbbb", "cccc", "dddd")); got != "aaaa · bbbb · cccc" {
		t.Errorf("joinFit = %q", got)
	}
	if got := ansi.Strip(joinFit(6, "a long first part", "b")); got != "a lon…" {
		t.Errorf("the first part alone should truncate: %q", got)
	}
}

func TestToolsLineKeepsSkillNamesWhenTwoToolsFit(t *testing.T) {
	u := sampleUsage()["claude:A"]
	for w, want := range map[int]string{
		84: "Bash 41 · Read 22 · Edit 9 · Agent 3 · Glob 2 · +3 · skills pr-comments, writeup",
		55: "Bash 41 · Read 22 · +6 · skills pr-comments, writeup",
		45: "Bash 41 · Read 22 · Edit 9 · +5 · 2 skills",
	} {
		if got := ansi.Strip(toolsLine(u, w)); got != want {
			t.Errorf("toolsLine at %d = %q, want %q", w, got, want)
		}
	}
}

func TestBarFillsByPercent(t *testing.T) {
	for pct, want := range map[int]string{0: "░░░░░░░░░░", 19: "▓▓░░░░░░░░", 50: "▓▓▓▓▓░░░░░", 100: "▓▓▓▓▓▓▓▓▓▓", 130: "▓▓▓▓▓▓▓▓▓▓"} {
		if got := ansi.Strip(bar(pct, 10)); got != want {
			t.Errorf("bar(%d) = %q, want %q", pct, got, want)
		}
	}
}

func TestFlowWrapsPartsUnderTheLabel(t *testing.T) {
	lines := flow("tools", []string{"Bash 41", "Read 22", "Edit 9", "Agent 3"}, 30)
	if len(lines) != 2 || !strings.HasPrefix(ansi.Strip(lines[0]), "tools    Bash 41 · Read 22") ||
		ansi.Strip(lines[1]) != "         Edit 9 · Agent 3" {
		t.Errorf("flow = %q", lines)
	}
}

// A quota window whose reset has passed says nothing: hive can't know the new
// percentage, so it shows only the windows that are still live.
func TestQuotaTextDropsAWindowWhoseResetPassed(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	staleFive := store.Quota{Tool: "claude", FiveUsed: 92, FiveReset: now.Add(-time.Hour).Unix(),
		SevenUsed: 30, SevenReset: now.Add(48 * time.Hour).Unix()}
	if got := ansi.Strip(quotaText(staleFive, now)); got != "7d 30% · resets 48h" {
		t.Errorf("quotaText with a stale 5h window = %q, want only the 7d one", got)
	}
	both := store.Quota{Tool: "claude", FiveUsed: 92, FiveReset: now.Add(-time.Hour).Unix(),
		SevenUsed: 30, SevenReset: now.Add(-time.Minute).Unix()}
	if got := quotaText(both, now); got != "" {
		t.Errorf("quotaText with both windows stale = %q, want nothing", got)
	}
}
