package gitx

import (
	"strings"
	"testing"
)

func findFile(files []FileChange, path string) (FileChange, bool) {
	for _, f := range files {
		if f.Path == path {
			return f, true
		}
	}
	return FileChange{}, false
}

func TestCommittedListsTheBranch(t *testing.T) {
	dir := gitRepo(t)
	files, diff, err := Committed(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	b, ok := findFile(files, "b.txt")
	if !ok || b.Status != "M" || b.Added != 1 || b.Removed != 1 {
		t.Errorf("b.txt = %+v, %v; want M +1 -1", b, ok)
	}
	c, ok := findFile(files, "c.txt")
	if !ok || c.Status != "A" || c.Added != 1 || c.Removed != 0 {
		t.Errorf("c.txt = %+v, %v; want A +1", c, ok)
	}
	if !strings.Contains(diff, "+feature") || !strings.Contains(diff, "+see") {
		t.Errorf("diff lacks the branch's lines:\n%s", diff)
	}
}

func TestUncommittedChangesListsTrackedAndUntracked(t *testing.T) {
	dir := gitRepo(t)
	git(t, dir, "checkout", "-q", "main")
	if files, diff, err := UncommittedChanges(dir); err != nil || len(files) != 0 || diff != "" {
		t.Fatalf("clean = %+v, %q, %v", files, diff, err)
	}
	write(t, dir, "a.txt", "main\nchanged\n")
	write(t, dir, "new.txt", "hello\n")
	files, diff, err := UncommittedChanges(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, ok := findFile(files, "a.txt")
	if !ok || a.Status != "M" || a.Added != 1 || a.Removed != 0 {
		t.Errorf("a.txt = %+v, %v; want M +1", a, ok)
	}
	n, ok := findFile(files, "new.txt")
	if !ok || n.Status != "?" {
		t.Errorf("new.txt = %+v, %v; want untracked", n, ok)
	}
	if !strings.Contains(diff, "+changed") {
		t.Errorf("diff lacks the tracked change:\n%s", diff)
	}
}

func TestAheadOfUpstreamWithoutOne(t *testing.T) {
	dir := gitRepo(t)
	n, err := AheadOfUpstream(dir)
	if err != nil || n != 0 {
		t.Errorf("AheadOfUpstream = %d, %v; want 0 with no upstream", n, err)
	}
}

func TestIsRepo(t *testing.T) {
	dir := gitRepo(t)
	if !IsRepo(dir) {
		t.Error("IsRepo said no in a repository")
	}
	if IsRepo(t.TempDir()) {
		t.Error("IsRepo said yes outside a repository")
	}
}
