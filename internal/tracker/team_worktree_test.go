package tracker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
)

// scratchRepo makes a git repository with one commit on main, in a folder of
// its own. git's settings and hive's data folder point at a temporary home,
// so the user's can't interfere and nothing lands in theirs.
func scratchRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	home := t.TempDir()
	for k, v := range map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"), "HIVE_HOME": filepath.Join(home, "hive"),
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": filepath.Join(home, ".gitconfig"),
		"GIT_AUTHOR_NAME": "hive test", "GIT_AUTHOR_EMAIL": "test@hive.invalid",
		"GIT_COMMITTER_NAME": "hive test", "GIT_COMMITTER_EMAIL": "test@hive.invalid",
	} {
		t.Setenv(k, v)
	}
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	repo := t.TempDir()
	mustGit(t, repo, "init", "-q", "-b", "main")
	commitFile(t, repo, "README", "hello")
	return repo
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// commitFile writes a file in dir and commits it there.
func commitFile(t *testing.T, dir, name, text string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, name), text)
	mustGit(t, dir, "add", name)
	mustGit(t, dir, "commit", "-q", "-m", "add "+name)
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// worktreeTeam starts a worktrees team "api" in dir, led by the agent that
// makes it, with members roles. No member's tool exists, so none starts: the
// worktrees are made all the same, before the windows would open.
func worktreeTeam(t *testing.T, tr *Tracker, dir string, roles ...string) store.Team {
	t.Helper()
	o := TeamOptions{Name: "api", Cwd: dir, Worktrees: true, SelfLead: &store.Session{ID: "claude:C", Tool: "claude"}}
	for _, r := range roles {
		o.Members = append(o.Members, team.Spec{Role: r, Tool: "codx"})
	}
	tm, _, err := tr.NewTeam(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func memberCwd(t *testing.T, tr *Tracker, tm store.Team, role string) string {
	t.Helper()
	ms, err := tr.Store.Members(tm.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.Role == role && m.Active() {
			return m.Cwd
		}
	}
	t.Fatalf("no %s on %s", role, tm.Name)
	return ""
}

func branches(t *testing.T, repo string) string {
	t.Helper()
	return strings.Join(strings.Fields(mustGit(t, repo, "branch", "--list", "--format=%(refname:short)", "hive/*")), " ")
}

func TestWorktreesNeedARepositoryWithACommit(t *testing.T) {
	tr, _ := newWorld(t)
	scratchRepo(t)
	empty := t.TempDir()
	mustGit(t, empty, "init", "-q")
	for _, c := range []struct{ name, dir, want string }{
		{"api", t.TempDir(), "isn't in one"},
		{"api", empty, "has none yet"},
		{"a..b", scratchRepo(t), "pick another team name"},
	} {
		o := TeamOptions{Name: c.name, Cwd: c.dir, Worktrees: true, SelfLead: &store.Session{ID: "claude:C", Tool: "claude"},
			Members: []team.Spec{{Role: "backend", Tool: "codx"}}}
		if _, _, err := tr.NewTeam(context.Background(), o); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s in %s: NewTeam = %v, want %q", c.name, c.dir, err, c.want)
		}
	}
	if teams, _ := tr.Store.Teams(); len(teams) != 0 {
		t.Fatalf("teams = %+v, want none recorded", teams)
	}
}

func TestEachMemberGetsAWorktreeAndTheLeadTheFolder(t *testing.T) {
	tr, _ := newWorld(t)
	repo := scratchRepo(t)
	commitFile(t, repo, "web/index.html", "<p>")
	head := mustGit(t, repo, "rev-parse", "HEAD")
	tm := worktreeTeam(t, tr, filepath.Join(repo, "web"), "backend", "frontend")

	if tm.Workspace != store.WorkspaceWorktrees {
		t.Errorf("workspace = %q", tm.Workspace)
	}
	if b, ok, _ := tr.Store.TeamBase(tm.ID); !ok || b.Branch != "main" || b.Head != head {
		t.Errorf("base = %+v, want main at %s", b, head)
	}
	if got := memberCwd(t, tr, tm, "lead"); got != "" {
		t.Errorf("the lead works in %s, want the team's folder", got)
	}
	for _, role := range []string{"backend", "frontend"} {
		// The member starts in the worktree's copy of the team's folder.
		want := filepath.Join(WorktreeDir("api", role), "web")
		if got := memberCwd(t, tr, tm, role); got != want {
			t.Errorf("%s works in %s, want %s", role, got, want)
		}
		if got := mustGit(t, want, "rev-parse", "--abbrev-ref", "HEAD"); got != "hive/api/"+role {
			t.Errorf("%s is on %s", role, got)
		}
		if _, err := os.Stat(filepath.Join(want, "index.html")); err != nil {
			t.Errorf("%s's worktree lacks the team folder's files: %v", role, err)
		}
	}
	if got := branches(t, repo); got != "hive/api/backend hive/api/frontend" {
		t.Errorf("branches = %q", got)
	}
}

func TestATeamThatIsNotKeptTakesItsWorktreesWithIt(t *testing.T) {
	tr, _ := newWorld(t)
	repo := scratchRepo(t)
	o := TeamOptions{Name: "api", Cwd: repo, Worktrees: true, Lead: &team.Spec{Tool: "claud"},
		Members: []team.Spec{{Role: "backend", Tool: "codx"}}}
	if _, _, err := tr.NewTeam(context.Background(), o); err == nil {
		t.Fatal("a team none of whose members started was kept")
	}
	if got := branches(t, repo); got != "" {
		t.Errorf("branches left: %q", got)
	}
	if _, err := os.Stat(filepath.Dir(WorktreeDir("api", "backend"))); !os.IsNotExist(err) {
		t.Errorf("the team's worktrees are still there: %v", err)
	}
	// The name and its branches are free for the next try.
	worktreeTeam(t, tr, repo, "backend")
}

func TestAWorktreeThatIsThereAlreadyIsNotTaken(t *testing.T) {
	tr, _ := newWorld(t)
	repo := scratchRepo(t)
	mustGit(t, repo, "branch", "hive/api/backend") // left by an older team of the same name
	writeFile(t, filepath.Join(WorktreeDir("api", "frontend"), "notes"), "mine")
	tm := worktreeTeam(t, tr, repo, "backend", "frontend")

	msgs, _ := tr.Store.Messages(tm.ID, 50, false)
	var posts []string
	for _, m := range msgs {
		posts = append(posts, m.Body)
	}
	all := strings.Join(posts, "|")
	for _, want := range []string{"backend didn't start: the branch hive/api/backend is there already",
		"frontend didn't start: " + WorktreeDir("api", "frontend") + " is there already"} {
		if !strings.Contains(all, want) {
			t.Errorf("channel lacks %q:\n%s", want, all)
		}
	}
	if wts, _ := tr.Store.Worktrees(tm.ID); len(wts) != 0 {
		t.Errorf("worktrees = %+v, want none taken", wts)
	}
}

func TestAMemberAddedLaterStartsFromTheFolderAsItIsNow(t *testing.T) {
	tr, _ := newWorld(t)
	repo := scratchRepo(t)
	tm := worktreeTeam(t, tr, repo, "backend")
	commitFile(t, repo, "merged.go", "package x") // the lead merged something

	ctx := context.Background()
	if _, _, err := tr.AddMember(ctx, tm, team.Spec{Role: "docs", Tool: "codx"}, ""); err == nil {
		t.Fatal("codx started")
	}
	docs := memberCwd(t, tr, tm, "docs")
	if _, err := os.Stat(filepath.Join(docs, "merged.go")); err != nil {
		t.Fatalf("docs's worktree isn't from the folder's HEAD now: %v", err)
	}

	// Taken off the team and put back, it finds its work where it left it.
	writeFile(t, filepath.Join(docs, "draft.md"), "half done")
	if err := tr.RetireMember(tm, "docs"); err != nil {
		t.Fatal(err)
	}
	tr.AddMember(ctx, tm, team.Spec{Role: "docs", Tool: "codx"}, "")
	if got := memberCwd(t, tr, tm, "docs"); got != docs {
		t.Fatalf("docs is back in %s, want %s", got, docs)
	}
	if _, err := os.Stat(filepath.Join(docs, "draft.md")); err != nil {
		t.Fatalf("docs's work is gone: %v", err)
	}
}

func TestCleanRemovesOnlyTheMergedWorktrees(t *testing.T) {
	tr, _ := newWorld(t)
	repo := scratchRepo(t)
	tm := worktreeTeam(t, tr, repo, "backend", "frontend", "reviewer", "docs")
	wt := func(role string) string { return WorktreeDir("api", role) }

	commitFile(t, wt("backend"), "api.go", "package api")
	mustGit(t, repo, "merge", "-q", "--no-edit", "hive/api/backend") // the lead merges backend
	commitFile(t, wt("frontend"), "app.js", "app()")                 // frontend's isn't merged
	writeFile(t, filepath.Join(wt("reviewer"), "README"), "edited")  // reviewer never committed
	// docs did nothing at all.

	states, base, err := tr.TeamWorktrees(tm)
	if err != nil || base != "main" || len(states) != 4 {
		t.Fatalf("TeamWorktrees = %+v, %q, %v", states, base, err)
	}
	if got := states[1].State(); got != "1 commit to merge" {
		t.Errorf("frontend = %q", got)
	}

	d, err := tr.DoneTeam(tm, store.FromYou, true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, w := range d.Worktrees {
		got[w.Role] = w.State()
	}
	want := map[string]string{"backend": "removed", "frontend": "1 commit to merge",
		"reviewer": "merged; changes not committed", "docs": "removed"}
	for role, w := range want {
		if got[role] != w {
			t.Errorf("%s: %q, want %q", role, got[role], w)
		}
	}
	if got := branches(t, repo); got != "hive/api/frontend hive/api/reviewer" {
		t.Errorf("branches = %q, want the unmerged ones kept", got)
	}
	for role, gone := range map[string]bool{"backend": true, "docs": true, "frontend": false, "reviewer": false} {
		if _, err := os.Stat(wt(role)); os.IsNotExist(err) != gone {
			t.Errorf("%s's worktree gone = %v, want %v", role, os.IsNotExist(err), gone)
		}
	}
	if left, _ := tr.Store.Worktrees(tm.ID); len(left) != 2 {
		t.Errorf("worktrees on record = %+v, want frontend and reviewer for the board", left)
	}
	if tm, _ = tr.Store.TeamByID(tm.ID); tm.Status != store.TeamDone {
		t.Errorf("status = %q", tm.Status)
	}

	// Once they are dealt with, cleaning again takes the rest, and the team's folder of worktrees.
	mustGit(t, repo, "merge", "-q", "--no-edit", "hive/api/frontend")
	mustGit(t, wt("reviewer"), "checkout", "--", "README")
	if d, err = tr.DoneTeam(tm, store.FromYou, true); err != nil || !d.Already {
		t.Fatalf("DoneTeam again = %+v, %v", d, err)
	}
	if got := branches(t, repo); got != "" {
		t.Errorf("branches = %q, want none", got)
	}
	if _, err := os.Stat(filepath.Dir(wt("backend"))); !os.IsNotExist(err) {
		t.Errorf("the team's worktrees folder is still there: %v", err)
	}
}

func TestCleanAgainstADetachedHeadUsesTheFoldersHeadNow(t *testing.T) {
	tr, _ := newWorld(t)
	repo := scratchRepo(t)
	mustGit(t, repo, "checkout", "-q", "--detach")
	tm := worktreeTeam(t, tr, repo, "backend")
	commitFile(t, WorktreeDir("api", "backend"), "api.go", "package api")
	mustGit(t, repo, "merge", "-q", "--no-edit", "hive/api/backend")

	d, err := tr.DoneTeam(tm, store.FromYou, true)
	if err != nil || len(d.Worktrees) != 1 || !d.Worktrees[0].Removed || !strings.Contains(d.Base, "HEAD") {
		t.Fatalf("DoneTeam = %+v, %v; want backend removed against the folder's HEAD", d, err)
	}
}

func TestCleanKeepsTheWorktreeOfAMemberThatRuns(t *testing.T) {
	tr, w := newWorld(t)
	repo := scratchRepo(t)
	tm := worktreeTeam(t, tr, repo, "backend")
	// The backend runs: Claude A, pid 100, in pane %1.
	tr.Store.PutLaunchTeam(store.LaunchTeam{Pane: "%1", TeamID: tm.ID, Role: "backend", CreatedAt: w.now})
	claudeHook(t, tr, w, 100, "A", agent.Start)

	d, err := tr.DoneTeam(tm, store.FromYou, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(d.Running, ",") != "backend" {
		t.Errorf("running = %v, want backend", d.Running)
	}
	if len(d.Worktrees) != 1 || d.Worktrees[0].Removed || d.Worktrees[0].Note != "it still runs" {
		t.Fatalf("worktrees = %+v, want backend's kept while it runs", d.Worktrees)
	}
	if _, err := os.Stat(WorktreeDir("api", "backend")); err != nil {
		t.Errorf("backend's worktree went from under it: %v", err)
	}
}

func TestADoneTeamTakesNoMoreMail(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)
	claudeHook(t, tr, w, 100, "A", agent.Prompt) // working
	tr.Say(tm, "lead", "backend", "one more thing", false)

	d, err := tr.DoneTeam(tm, "lead", false)
	if err != nil || d.Dropped != 1 || strings.Join(d.Running, ",") != "backend" {
		t.Fatalf("DoneTeam = %+v, %v; want the queued message dropped and backend still running", d, err)
	}
	if !strings.Contains(hivePosts(tr), "team done, by lead") {
		t.Errorf("hive posts = %q", hivePosts(tr))
	}
	tm = theTeam(t, tr)
	if _, _, err := tr.Say(tm, "lead", "backend", "hello?", false); err == nil || !strings.Contains(err.Error(), "is done") {
		t.Errorf("Say = %v, want it refused", err)
	}
	if _, _, err := tr.Post(tm, "backend", "@lead one more"); err == nil {
		t.Error("a post went to a done team")
	}
	if _, _, err := tr.AddMember(context.Background(), tm, team.Spec{Role: "docs", Tool: "claude"}, ""); err == nil {
		t.Error("a member joined a done team")
	}
	claudeHook(t, tr, w, 100, "A", agent.Idle)
	if how, _ := tr.Deliver("claude:A", 0); len(*sent) != 0 {
		t.Fatalf("Deliver = %q, typed %+v into a member of a done team", how, *sent)
	}
}
