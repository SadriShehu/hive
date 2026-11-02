package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/gitx"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
)

func worktreeState(role, branch string) tracker.WorktreeState {
	return tracker.WorktreeState{
		Worktree: store.Worktree{Role: role, Branch: branch},
		Ahead:    3, Behind: 1,
		Diff:        tracker.DiffStat{Files: 5, Added: 210, Removed: 34},
		Uncommitted: 2,
		Merge:       gitx.Clean,
	}
}

// A branch line words the branch, its distance from the base, what it changes
// and how it merges; only the merge result takes a colour.
func TestBranchLineWordsTheBranch(t *testing.T) {
	st := worktreeState("backend", "hive/api-v2/backend")
	got := branchLine(st, 120)
	plain := ansi.Strip(got)
	for _, want := range []string{"⎇ hive/api-v2/backend", "↑3 ↓1", "5 files +210 −34", "2 uncommitted", "✓ merges clean"} {
		if !strings.Contains(plain, want) {
			t.Errorf("branch line lacks %q:\n%s", want, plain)
		}
	}
	if !strings.HasSuffix(got, sOK.Render("✓ merges clean")) {
		t.Errorf("the clean result isn't green: %q", got)
	}

	conflict := st
	conflict.Behind = 0
	conflict.Merge, conflict.Conflicts = gitx.Conflicts, []string{"web/app.tsx", "api/x.go"}
	got = branchLine(conflict, 120)
	if plain := ansi.Strip(got); !strings.Contains(plain, "✗ conflicts: web/app.tsx (+1)") {
		t.Errorf("conflict line = %q", plain)
	}
	if !strings.HasSuffix(got, sError.Render("✗ conflicts: web/app.tsx (+1)")) {
		t.Errorf("conflicts aren't red: %q", got)
	}

	merged := st
	merged.Ahead, merged.Behind, merged.Diff, merged.Uncommitted = 0, 2, tracker.DiffStat{}, 0
	merged.Merge = gitx.Merged
	if plain := ansi.Strip(branchLine(merged, 120)); !strings.Contains(plain, "merged ✓") || strings.Contains(plain, "↑") {
		t.Errorf("merged line = %q", plain)
	}
}

// The diff stat goes first, then how far behind the branch is, then the branch
// name is cut; the merge result always stays.
func TestBranchLineDropsInOrder(t *testing.T) {
	st := worktreeState("backend", "hive/api-v2/backend")
	wide := ansi.Strip(branchLine(st, 120))
	if !strings.Contains(wide, "5 files") || !strings.Contains(wide, "↓1") {
		t.Fatalf("wide line lost detail: %q", wide)
	}
	narrow := ansi.Strip(branchLine(st, 60))
	if strings.Contains(narrow, "5 files") {
		t.Errorf("60 wide kept the diff stat: %q", narrow)
	}
	if !strings.Contains(narrow, "↓1") {
		t.Errorf("60 wide dropped the behind count before it had to: %q", narrow)
	}
	very := ansi.Strip(branchLine(st, 42))
	if strings.Contains(very, "↓1") {
		t.Errorf("42 wide kept the behind count: %q", very)
	}
	if !strings.Contains(very, "↑3") {
		t.Errorf("42 wide lost the ahead count too: %q", very)
	}
	tiny := ansi.Strip(branchLine(st, 30))
	if !strings.Contains(tiny, "✓ merges clean") {
		t.Errorf("30 wide lost the merge result: %q", tiny)
	}
	if strings.Contains(tiny, "hive/api-v2/backend") {
		t.Errorf("30 wide didn't cut the branch name: %q", tiny)
	}
}

// A team's branch lines draw nothing until git has answered, then one under
// each member on a worktrees team.
func TestTeamHeadDrawsBranchesOnlyOnceFetched(t *testing.T) {
	m, ops := setupTeam(t)
	ops.snap.Teams[0].Workspace = store.WorkspaceWorktrees
	m = step(t, m, refreshedMsg{sessions: ops.sessions, snap: ops.snap})
	team := m.snap.Teams[0]
	m.branches = nil

	if lines := strings.Join(m.teamHead(team, 80), "\n"); strings.Contains(lines, "⎇") {
		t.Errorf("the head drew a branch before the fetch:\n%s", lines)
	}
	if under, shared, errLine := m.memberBranches(team, 80); under != nil || shared != "" || errLine != "" {
		t.Errorf("memberBranches before the fetch = %v, %q, %q", under, shared, errLine)
	}

	m.branches = map[string]branchFetch{"t1": {got: true, report: BranchReport{
		Worktrees: true,
		Base:      "feat/wave-2",
		Trees: []tracker.WorktreeState{
			{Worktree: store.Worktree{Role: "backend", Branch: "hive/api-v2/backend"},
				Ahead: 3, Merge: gitx.Clean, Diff: tracker.DiffStat{Files: 2, Added: 40, Removed: 2}},
			{Worktree: store.Worktree{Role: "frontend", Branch: "hive/api-v2/frontend"},
				Ahead: 1, Merge: gitx.Conflicts, Conflicts: []string{"web/app.tsx"}},
		},
	}}}
	lines := strings.Join(m.teamHead(team, 80), "\n")
	if !strings.Contains(lines, "⎇ hive/api-v2/backend") || !strings.Contains(lines, "⎇ hive/api-v2/frontend") {
		t.Errorf("the head lacks a member's branch:\n%s", lines)
	}
	if !strings.Contains(lines, "✗ conflicts: web/app.tsx") {
		t.Errorf("the head lacks the conflict:\n%s", lines)
	}
}

// A shared-folder team gets one line under its roster, and a git error is
// said with ⎇ ? in dim text.
func TestSharedFolderAndErrorLines(t *testing.T) {
	m, _ := setupTeam(t)
	team := m.snap.Teams[0]

	m.branches = map[string]branchFetch{"t1": {got: true, report: BranchReport{Head: "feat/x", Uncommitted: 4}}}
	if plain := ansi.Strip(strings.Join(m.teamHead(team, 80), "\n")); !strings.Contains(plain, "⎇ feat/x · 4 files uncommitted") {
		t.Errorf("shared line missing:\n%s", plain)
	}

	m.branches["t1"] = branchFetch{got: true, err: "not a git repository"}
	plain := ansi.Strip(strings.Join(m.teamHead(team, 80), "\n"))
	if !strings.Contains(plain, "⎇ ? not a git repository") {
		t.Errorf("error line missing:\n%s", plain)
	}
}

// Selecting another team starts a fresh fetch; a report younger than ten
// seconds is reused.
func TestBranchesAreFetchedAtMostEveryTenSeconds(t *testing.T) {
	m, _ := setupTeam(t)
	m.branches = nil
	if cmd := m.branchesCmd(); cmd == nil {
		t.Fatal("the selected team's branches weren't fetched")
	}
	if cmd := m.branchesCmd(); cmd != nil {
		t.Error("a second fetch started before ten seconds passed")
	}
	m.now = func() time.Time { return now.Add(11 * time.Second) }
	if cmd := m.branchesCmd(); cmd == nil {
		t.Error("no fetch after ten seconds")
	}
}

// Selecting a member asks for its team's branches, not the session's own.
func TestBranchFetchFollowsTheSelection(t *testing.T) {
	m, _ := setupTeam(t)
	m = step(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if team, ok := m.branchTeam(); !ok || team.ID != "t1" {
		t.Fatalf("branchTeam = %+v, %v; want t1", team, ok)
	}
}
