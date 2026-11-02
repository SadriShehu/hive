package cmd

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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

// A long goal and a long held-task title are printed whole, not cut short.
func TestTeamTextIsNotCutShort(t *testing.T) {
	goal := "add the admin API " + strings.Repeat("with a long description ", 6) + "END"
	if got := teamRowText("api", "active", goal); !strings.Contains(got, goal) {
		t.Errorf("ls line = %q, want the whole goal", got)
	}
	title := "write the docs " + strings.Repeat("for every endpoint ", 5) + "END"
	if got := memberBoardText("backend", "claude", "working", 2, store.Task{N: 4, Title: title}, true, "", 0, false); !strings.Contains(got, title) {
		t.Errorf("board line = %q, want the whole held title", got)
	}
}

func TestBoardShowsEachMembersPart(t *testing.T) {
	got := memberBoardText("backend", "claude", "working", 2, store.Task{}, false,
		"own the parsers\nand the routes", 200, false)
	if !strings.Contains(got, "\n  own the parsers and the routes") {
		t.Errorf("board line = %q, want the part on one dim line", got)
	}

	long := strings.Repeat("x", 200)
	got = memberBoardText("backend", "claude", "working", 0, store.Task{}, false, long, 40, false)
	lines := strings.Split(got, "\n")
	if len(lines) != 2 {
		t.Fatalf("board line = %q, want the member's line and its part", got)
	}
	if n := utf8.RuneCountInString(lines[1]); n != 40 || !strings.HasSuffix(lines[1], "…") {
		t.Errorf("part line = %q (%d runes), want it cut to 40 with …", lines[1], n)
	}
}

func TestPartFlagNamesTheMembersParts(t *testing.T) {
	if newTeamNewCmd().Flags().Lookup("part") == nil {
		t.Fatal("team new has no --part flag")
	}
	specs := []team.Spec{
		{Role: "backend", Tool: "claude", Prompt: "from the template"},
		{Role: "frontend", Tool: "codex"},
	}
	got, err := applyParts(specs, []string{"backend=own the handlers", "frontend=own the web UI"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Prompt != "own the handlers" || got[1].Prompt != "own the web UI" {
		t.Errorf("specs = %+v, want --part to set each part", got)
	}
	if got[1].Tool != "codex" {
		t.Errorf("--part changed a member's tool: %+v", got[1])
	}

	_, err = applyParts([]team.Spec{{Role: "backend"}}, []string{"mobile=own the app"})
	want := `no member called "mobile" in --member; roles: backend`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestTaskEditChangesOnlyTheFlagsItWasGiven(t *testing.T) {
	task := store.Task{N: 3, Title: "old", Body: "old body", Owner: "backend", After: []int{1, 2},
		Status: store.TaskClaimed, Note: "working"}
	got, err := editValues(task, map[string]bool{"title": true}, "new title", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "new title" || got.Body != "old body" || got.Owner != "backend" ||
		len(got.After) != 2 || got.Status != store.TaskClaimed {
		t.Errorf("a title edit = %+v, want the rest kept", got)
	}

	got, err = editValues(task, map[string]bool{"owner": true}, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "" || got.Title != "old" || len(got.After) != 2 {
		t.Errorf("an owner edit = %+v, want --owner \"\" to clear only the owner", got)
	}

	got, err = editValues(task, map[string]bool{"after": true}, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.After) != 0 || got.Title != "old" || got.Owner != "backend" {
		t.Errorf("an after edit = %+v, want --after \"\" to clear only the waits", got)
	}

	got, err = editValues(task, map[string]bool{"body": true}, "", "new body", "", "")
	if err != nil || got.Body != "new body" || got.Title != "old" {
		t.Errorf("a body edit = %+v, %v", got, err)
	}
}

func TestTeamTaskHasEditAndReopen(t *testing.T) {
	c := newTeamTaskCmd()
	found := map[string]bool{}
	for _, sub := range c.Commands() {
		found[sub.Name()] = true
		switch sub.Name() {
		case "edit":
			for _, f := range []string{"title", "body", "owner", "after"} {
				if sub.Flags().Lookup(f) == nil {
					t.Errorf("task edit has no --%s", f)
				}
			}
		case "reopen":
			if sub.Flags().Lookup("why") == nil {
				t.Error("task reopen has no --why")
			}
		}
	}
	for _, name := range []string{"edit", "reopen"} {
		if !found[name] {
			t.Errorf("team task has no %s command", name)
		}
	}
}

func TestWaitingNoteNamesTheSenders(t *testing.T) {
	got := waitingNote(3, map[string]int{"lead": 2, "manager": 1})
	want := "✉ 3 messages wait for you (lead ×2, manager) · hive team inbox"
	if got != want {
		t.Errorf("waitingNote = %q, want %q", got, want)
	}
	if got := waitingNote(1, map[string]int{"lead": 1}); got != "✉ 1 message waits for you (lead) · hive team inbox" {
		t.Errorf("one message = %q", got)
	}
	if got := waitingNote(0, nil); got != "" {
		t.Errorf("no mail = %q, want nothing", got)
	}
}

func TestInboxHasPeek(t *testing.T) {
	c := newTeamInboxCmd()
	if c.Flags().Lookup("peek") == nil {
		t.Error("inbox has no --peek")
	}
	if newTeamCmd().PersistentPostRun == nil {
		t.Error("team commands don't say when mail waits")
	}
}
