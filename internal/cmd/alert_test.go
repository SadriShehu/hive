package cmd

import (
	"slices"
	"testing"

	"github.com/sadrishehu/hive/internal/adapters"
)

func TestAlertCommandSubstitutesEveryPlaceholder(t *testing.T) {
	got := alertCommand([]string{
		"ntfy", "pub", "my-topic",
		"{title}: {text}",
		"id={id}", "cwd={cwd}", "nothing",
	}, "claude ‹fix the tests› needs you", "hive", "claude:abc", "/src/app")
	want := []string{
		"ntfy", "pub", "my-topic",
		"hive: claude ‹fix the tests› needs you",
		"id=claude:abc", "cwd=/src/app", "nothing",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("alertCommand = %q, want %q", got, want)
	}
}

func TestNotifyCommandNeedsAProgram(t *testing.T) {
	for _, argv := range [][]string{nil, {""}} {
		if err := notifyCommand(argv); err == nil {
			t.Errorf("notifyCommand(%q) = nil, want an error", argv)
		}
	}
}

func TestAlertsTextNamesTheCommand(t *testing.T) {
	if got := alertsText(adapters.Alerts{Command: []string{"ntfy"}}); got != "a command" {
		t.Errorf("alertsText = %q, want a command", got)
	}
	got := alertsText(adapters.Alerts{Tmux: true, Command: []string{"ntfy"}})
	if got != "a tmux message and a command" {
		t.Errorf("alertsText = %q, want the tmux message and a command", got)
	}
}
