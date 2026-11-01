package cmd

import (
	"path/filepath"
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
		err  string
	}{
		{"a member, on its team", teamEnv{caller: "claude:A", roles: backend}, api, "", "backend", ""},
		{"a member, on another team", teamEnv{caller: "claude:A", roles: backend}, web, "", "api/backend", ""},
		{"an agent on no team", teamEnv{caller: "claude:C"}, api, "", "claude:C", ""},
		{"a shell outside any agent", teamEnv{}, api, "", store.FromYou, ""},
		{"--as, a role it plays", teamEnv{caller: "claude:A", roles: backend}, api, "backend", "backend", ""},
		{"--as, a script's", teamEnv{}, api, "lead", "lead", ""},
		{"--as you, from an agent", teamEnv{caller: "claude:A", roles: backend}, api, store.FromYou, "", "can't write as you"},
		{"--as another role, from an agent", teamEnv{caller: "claude:A", roles: backend}, api, "lead", "", "plays no such role"},
		{"--as its role, on another team", teamEnv{caller: "claude:A", roles: backend}, web, "backend", "", "plays no such role"},
	} {
		got, err := c.e.sender(c.to, c.as)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%s: sender = %q, %v; want an error naming %q", c.name, got, err, c.err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: sender = %q, %v, want %q", c.name, got, err, c.want)
		}
	}
}

func TestRoleOnRefusesAnAgentsAs(t *testing.T) {
	api := store.Team{ID: "t1", Name: "api"}
	backend := []tracker.Role{{Team: api, Member: store.Member{TeamID: "t1", Role: "backend"}}}
	agent := teamEnv{caller: "claude:A", roles: backend}
	if got, err := agent.roleOn(api, ""); err != nil || got != "backend" {
		t.Errorf("the agent's role = %q, %v; want backend", got, err)
	}
	if got, err := agent.roleOn(api, "backend"); err != nil || got != "backend" {
		t.Errorf("--as its role = %q, %v; want backend", got, err)
	}
	for _, as := range []string{store.FromYou, "lead"} {
		if got, err := agent.roleOn(api, as); err == nil {
			t.Errorf("an agent waited as %q = %q, want it refused", as, got)
		}
	}
	script := teamEnv{}
	if got, err := script.roleOn(api, "lead"); err != nil || got != "lead" {
		t.Errorf("a script's --as = %q, %v; want lead", got, err)
	}
}

// A message to a working member waits until it next goes idle, not until its
// current turn ends: the help must say so on the team and say commands.
func TestTeamHelpSaysMailWaitsUntilItGoesIdle(t *testing.T) {
	team := newTeamCmd()
	check := func(name, long string) {
		t.Helper()
		if strings.Contains(long, "turn ends") || !strings.Contains(long, "goes idle") {
			t.Errorf("%s help = %q, want it to say the message waits until the member goes idle", name, long)
		}
	}
	check("team", team.Long)
	for _, c := range team.Commands() {
		if c.Name() == "say" {
			check("say", c.Long)
			return
		}
	}
	t.Fatal("the team command has no say subcommand")
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

func TestTheFeedHidesPrivateMessages(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tm := store.Team{ID: "t1", Name: "api", Cwd: "/work", Status: store.TeamActive, CreatedAt: 1, UpdatedAt: 1}
	if err := st.CreateTeam(tm); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddMessage(store.Message{TeamID: tm.ID, From: "lead", Body: "public", Kind: store.MsgPost, At: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddMessage(store.Message{TeamID: tm.ID, From: "lead", To: "backend", Body: "secret", Kind: store.MsgSay, Private: true, At: 2}, []string{"backend"}); err != nil {
		t.Fatal(err)
	}

	msgs, err := feed(st, tm.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Body != "public" {
		t.Fatalf("feed = %+v, want only the public post", msgs)
	}
	pending, err := st.Pending(tm.ID, "backend")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Body != "secret" {
		t.Fatalf("inbox = %+v, want the private message", pending)
	}
}
