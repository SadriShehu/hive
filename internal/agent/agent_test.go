package agent

import (
	"strings"
	"testing"
)

func TestIsHeadless(t *testing.T) {
	opencode := Spec{Headless: []string{"run"}}
	claude := Spec{Headless: []string{"-p", "--print"}}
	tests := []struct {
		spec Spec
		argv string
		want bool
	}{
		{opencode, "opencode run -m x hi", true},
		{opencode, "opencode", false},
		{opencode, "opencode --prompt run the tests", false}, // "run" inside a prompt
		{claude, "claude -p hello", true},
		{claude, "claude --model haiku --print hi", true},
		{claude, "claude --resume abc", false},
	}
	for _, tt := range tests {
		if got := tt.spec.IsHeadless(strings.Fields(tt.argv)); got != tt.want {
			t.Errorf("IsHeadless(%q) = %v, want %v", tt.argv, got, tt.want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/Users/me/go/bin/hive": "/Users/me/go/bin/hive",
		"/opt/my tools/hive":    "'/opt/my tools/hive'",
		"/it's/hive":            `'/it'\''s/hive'`,
	} {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
