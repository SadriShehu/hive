package agent

import (
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/proc"
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

func TestMatchesSkipsHelperProcesses(t *testing.T) {
	codex := Spec{Process: []string{"codex"}, HelperSubcommands: []string{"app-server", "sandbox"}}
	tests := []struct {
		argv string
		want bool
	}{
		{"codex", true},
		{"/opt/homebrew/bin/codex resume abc", true},
		{"codex exec app-server", true},
		{"/Users/me/.codex/bin/codex app-server --listen unix://", false},
		{"codex sandbox -- ls", false},
		{"claude", false},
	}
	for _, tt := range tests {
		if got := codex.Matches(proc.Proc{Args: strings.Fields(tt.argv)}); got != tt.want {
			t.Errorf("Matches(%q) = %v, want %v", tt.argv, got, tt.want)
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

func TestExpand(t *testing.T) {
	tests := []struct {
		tmpl []string
		vars map[string]string
		want string
	}{
		{[]string{"opencode", "--prompt", "{prompt}"}, nil, "opencode"},
		{[]string{"opencode", "--prompt", "{prompt}"}, map[string]string{"prompt": "fix it"}, "opencode --prompt fix it"},
		{[]string{"claude", "--session-id", "{session}", "{prompt}"}, map[string]string{"session": "u1"}, "claude --session-id u1"},
		{[]string{"claude", "--resume", "{id}", "{prompt}"}, map[string]string{"id": "abc", "prompt": "go on"}, "claude --resume abc go on"},
		{[]string{"opencode", "--session", "{id}", "--prompt", "{prompt}"}, map[string]string{"id": "ses_1"}, "opencode --session ses_1"},
		{[]string{"tool", "--name=job-{id}"}, map[string]string{"id": "7"}, "tool --name=job-7"},
		{[]string{"claude", "--resume", "{id}", "{prompt}"}, map[string]string{"id": "abc", "prompt": "keep {id} literal"}, "claude --resume abc keep {id} literal"},
	}
	for _, tt := range tests {
		if got := strings.Join(Expand(tt.tmpl, tt.vars), " "); got != tt.want {
			t.Errorf("Expand(%v, %v) = %q, want %q", tt.tmpl, tt.vars, got, tt.want)
		}
	}
}
