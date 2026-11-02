package tracker

import (
	"path/filepath"
	"strings"
	"testing"
)

// A worktree member's changes are the commits on its branch since the team's
// base, then what is left uncommitted in its worktree.
func TestChangesReadsAMembersBranchAndWorktree(t *testing.T) {
	tr, _ := newWorld(t)
	repo := scratchRepo(t)
	tm := worktreeTeam(t, tr, repo, "backend")
	cwd := memberCwd(t, tr, tm, "backend")

	writeFile(t, filepath.Join(cwd, "work.txt"), "one\ntwo\n")
	mustGit(t, cwd, "add", "work.txt")
	mustGit(t, cwd, "commit", "-q", "-m", "work")
	writeFile(t, filepath.Join(cwd, "README"), "hello\nchanged\n")

	rep, err := tr.Changes(cwd, tm.ID, "backend")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Worktree || rep.Branch != "hive/api/backend" || rep.Base != "main" {
		t.Errorf("report = %+v, want backend's branch against main", rep)
	}
	if rep.Ahead != 1 || rep.Uncommitted != 1 {
		t.Errorf("ahead, uncommitted = %d, %d; want 1, 1", rep.Ahead, rep.Uncommitted)
	}
	if rep.BaseRef != "refs/heads/main" {
		t.Errorf("base ref = %q", rep.BaseRef)
	}
	if len(rep.Files) != 2 {
		t.Errorf("files = %+v, want work.txt and README", rep.Files)
	}
	for _, want := range []string{"work.txt", "+one", "+changed"} {
		if !strings.Contains(rep.Diff, want) {
			t.Errorf("diff lacks %q:\n%s", want, rep.Diff)
		}
	}
}

// A session in a plain folder shows its uncommitted changes, including
// untracked files by name.
func TestChangesReadsAPlainFolder(t *testing.T) {
	tr, _ := newWorld(t)
	repo := scratchRepo(t)
	writeFile(t, filepath.Join(repo, "README"), "hello\nmore\n")
	writeFile(t, filepath.Join(repo, "new.txt"), "new\n")

	rep, err := tr.Changes(repo, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Worktree || rep.NotRepo {
		t.Errorf("report = %+v, want a plain repository", rep)
	}
	if rep.Uncommitted != 2 {
		t.Errorf("uncommitted = %d, want 2", rep.Uncommitted)
	}
	if len(rep.Files) != 2 {
		t.Errorf("files = %+v", rep.Files)
	}
	if !strings.Contains(rep.Diff, "+more") {
		t.Errorf("diff lacks the tracked change:\n%s", rep.Diff)
	}
}

func TestChangesSaysWhenItIsNotARepository(t *testing.T) {
	tr, _ := newWorld(t)
	rep, err := tr.Changes(t.TempDir(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.NotRepo {
		t.Errorf("report = %+v, want NotRepo", rep)
	}
}
