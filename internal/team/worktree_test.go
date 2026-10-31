package team

import (
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/store"
)

func TestBriefOnAWorktreesTeam(t *testing.T) {
	members := []store.Member{{Role: "lead", Tool: "claude"}, {Role: "backend", Tool: "codex", Cwd: "/wt/api/backend"},
		{Role: "web", Tool: "claude", Cwd: "/wt/api/web"}, {Role: "old", LeftAt: 1}}
	tm := store.Team{Name: "api", Cwd: "/work", Workspace: store.WorkspaceWorktrees, LeadRole: "lead"}

	b := Brief(tm, members[1], members)
	for _, want := range []string{"in /wt/api/backend.", "on the branch hive/api/backend", "the lead merges your branch"} {
		if !strings.Contains(b, want) {
			t.Errorf("member brief lacks %q:\n%s", want, b)
		}
	}
	if strings.Contains(b, "same folder") {
		t.Errorf("member brief says the folder is shared:\n%s", b)
	}
	b = Brief(tm, members[0], members)
	for _, want := range []string{"in /work.", "You work in the team's folder", "(hive/api/backend, hive/api/web)", "Merge their branches"} {
		if !strings.Contains(b, want) {
			t.Errorf("lead brief lacks %q:\n%s", want, b)
		}
	}
	if strings.Contains(b, "hive/api/old") {
		t.Errorf("lead brief names a retired member's branch:\n%s", b)
	}

	tm.LeadRole = ""
	if b := Brief(tm, members[0], members); !strings.Contains(b, "hive/api/lead") || !strings.Contains(b, "the human merges") {
		t.Errorf("with the user leading, every member has a branch the human merges:\n%s", b)
	}
	tm.Workspace = store.WorkspaceShared
	if b := Brief(tm, members[0], members); !strings.Contains(b, "Everyone works in the same folder") {
		t.Errorf("shared brief = %q", b)
	}
}
