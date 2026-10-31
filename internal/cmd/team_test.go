package cmd

import (
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
	"github.com/sadrishehu/hive/internal/tracker"
)

func TestSenderNeverPassesAnAgentOffAsTheUser(t *testing.T) {
	api, web := store.Team{ID: "t1", Name: "api"}, store.Team{ID: "t2", Name: "web"}
	backend := []tracker.Role{{Team: api, Member: store.Member{TeamID: "t1", Role: "backend"}}}
	for _, c := range []struct {
		name string
		e    teamEnv
		to   store.Team
		as   string
		want string
	}{
		{"a member, on its team", teamEnv{caller: "claude:A", roles: backend}, api, "", "backend"},
		{"a member, on another team", teamEnv{caller: "claude:A", roles: backend}, web, "", "api/backend"},
		{"an agent on no team", teamEnv{caller: "claude:C"}, api, "", "claude:C"},
		{"a shell outside any agent", teamEnv{}, api, "", store.FromYou},
		{"--as", teamEnv{caller: "claude:C"}, api, "lead", "lead"},
	} {
		if got := c.e.sender(c.to, c.as); got != c.want {
			t.Errorf("%s: sender = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestTemplateTeamDefaultsAndOverrides(t *testing.T) {
	tpl := adapters.Team{
		Name:      "api",
		Goal:      "add the admin API",
		Lead:      "claude:auto",
		Cwd:       "/src/api",
		Worktrees: true,
		Members: []adapters.TeamMember{
			{Role: "backend", Tool: "claude", Model: "auto", Prompt: "handlers"},
		},
	}
	extra := []team.Spec{{Role: "reviewer", Tool: "codex"}}

	goal, lead, cwd, worktrees, specs, err := templateTeam(tpl, "", "claude", "", false, map[string]bool{}, extra)
	if err != nil {
		t.Fatal(err)
	}
	if goal != tpl.Goal || lead != tpl.Lead || cwd != tpl.Cwd || !worktrees {
		t.Errorf("defaults = %q, %q, %q, %v", goal, lead, cwd, worktrees)
	}
	want := []team.Spec{
		{Role: "backend", Tool: "claude", Model: "auto", Prompt: "handlers"},
		{Role: "reviewer", Tool: "codex"},
	}
	if len(specs) != len(want) {
		t.Fatalf("specs = %+v", specs)
	}
	for i := range want {
		if specs[i] != want[i] {
			t.Errorf("spec %d = %+v, want %+v", i, specs[i], want[i])
		}
	}

	changed := map[string]bool{"goal": true, "lead": true, "cwd": true, "worktrees": true}
	goal, lead, cwd, worktrees, specs, err = templateTeam(tpl, "flag goal", "none", "/flag", false, changed, extra)
	if err != nil {
		t.Fatal(err)
	}
	if goal != "flag goal" || lead != "none" || cwd != "/flag" || worktrees {
		t.Errorf("overrides = %q, %q, %q, %v", goal, lead, cwd, worktrees)
	}
	if len(specs) != 2 || specs[0].Role != "backend" {
		t.Errorf("a flag must not drop the template's members: %+v", specs)
	}
}

func TestTemplateTeamRejectsSelfLead(t *testing.T) {
	_, _, _, _, _, err := templateTeam(adapters.Team{Name: "api", Lead: "self"}, "", "claude", "", false, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "self") {
		t.Fatalf("err = %v", err)
	}
}

func TestFindTeamTemplateUnknown(t *testing.T) {
	cfg := adapters.Config{Teams: []adapters.Team{{Name: "api"}, {Name: "web"}}}
	_, err := findTeamTemplate(cfg, "mobile")
	if err == nil || !strings.Contains(err.Error(), "mobile") || !strings.Contains(err.Error(), "api, web") {
		t.Fatalf("err = %v", err)
	}

	_, err = findTeamTemplate(adapters.Config{}, "api")
	if err == nil || !strings.Contains(err.Error(), "api") || !strings.Contains(err.Error(), "[[team]]") {
		t.Fatalf("empty config err = %v", err)
	}
}
