package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
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
