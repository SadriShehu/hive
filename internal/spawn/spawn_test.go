package spawn

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/paths"
)

var specs = []agent.Spec{
	{Name: "claude", Process: []string{"claude"}, TitleFlags: []string{"-n", "--name"}, SessionFlags: []string{"-r", "--resume", "--session-id"}},
	{Name: "opencode", Process: []string{"opencode"}, TitleFlags: []string{"--title"}, SessionFlags: []string{"-s", "--session"}},
}

func TestFind(t *testing.T) {
	const base = "/work"
	tests := []struct {
		cmd  string
		want []Found
	}{
		{`opencode run -m deepseek/deepseek-flash --auto --title admin-phase2-backend "implement the plan"`,
			[]Found{{Tool: "opencode", Title: "admin-phase2-backend", Cwd: base}}},
		{`cd backend && opencode run --title 'a b' 'x'`,
			[]Found{{Tool: "opencode", Title: "a b", Cwd: "/work/backend"}}},
		{`(cd /srv/admin && opencode run --title=c z) &`,
			[]Found{{Tool: "opencode", Title: "c", Cwd: "/srv/admin"}}},
		{`opencode run -s ses_123 "continue"`,
			[]Found{{Tool: "opencode", NativeID: "ses_123", Cwd: base}}},
		{`OPENCODE_CONFIG=/tmp/c.json timeout 600 opencode run --title t x > log.txt 2>&1`,
			[]Found{{Tool: "opencode", Title: "t", Cwd: base}}},
		{`nohup opencode run --title bg "long job" &>/tmp/bg.log &`,
			[]Found{{Tool: "opencode", Title: "bg", Cwd: base}}},
		{"ls; opencode run --title a x\nopencode run --title b y",
			[]Found{{Tool: "opencode", Title: "a", Cwd: base}, {Tool: "opencode", Title: "b", Cwd: base}}},
		{`claude -p 'Say OK' --model haiku`,
			[]Found{{Tool: "claude", Cwd: base}}},
		{`/opt/homebrew/bin/claude --session-id 1234 -p "go"`,
			[]Found{{Tool: "claude", NativeID: "1234", Cwd: base}}},
		{`opencode run --title "$(date +%s)-job" "prompt with; semicolons && ampersands"`,
			[]Found{{Tool: "opencode", Title: "$(date +%s)-job", Cwd: base}}},
		{"opencode run \\\n  --title cont \\\n  'x'",
			[]Found{{Tool: "opencode", Title: "cont", Cwd: base}}},
		{"for i in 1 2; do opencode run --title p$i x & done; wait",
			[]Found{{Tool: "opencode", Title: "p$i", Cwd: base}}},
		{"if claude -p check; then echo ok; fi",
			[]Found{{Tool: "claude", Cwd: base}}},
		// Not invocations.
		{`echo opencode run --title nope`, nil},
		{`grep -rn claude .claude/settings.json`, nil},
		{`cat ~/.claude/projects/x.jsonl | head`, nil},
		{`opencode run --help 2>&1 | head -40`, nil},
		{`claude --version`, nil},
		{`# opencode run --title commented`, nil},
	}
	for _, tt := range tests {
		if got := Find(tt.cmd, base, specs); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Find(%q)\n got  %+v\n want %+v", tt.cmd, got, tt.want)
		}
	}
}

func TestChdir(t *testing.T) {
	home := paths.Home()
	for _, tt := range []struct{ cwd, arg, want string }{
		{"/a", "b", "/a/b"},
		{"/a", "../c", "/c"},
		{"/a", "/abs", "/abs"},
		{"/a", "~", home},
		{"/a", "~/x", filepath.Join(home, "x")},
		{"/a", "$DIR", "/a"},
		{"", "rel", ""},
	} {
		if got := chdir(tt.cwd, []string{tt.arg}); got != tt.want {
			t.Errorf("chdir(%q, %q) = %q, want %q", tt.cwd, tt.arg, got, tt.want)
		}
	}
}
