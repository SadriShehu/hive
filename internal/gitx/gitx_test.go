package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitRepo makes a repository with a main branch, a branch that merges clean
// into main, one that conflicts with it, and one that is already merged.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "test")
	write(t, dir, "a.txt", "one\n")
	write(t, dir, "b.txt", "bee\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "base")

	git(t, dir, "checkout", "-qb", "feature")
	write(t, dir, "b.txt", "feature\n")
	write(t, dir, "c.txt", "see\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "feature work")

	git(t, dir, "checkout", "-q", "main")
	git(t, dir, "checkout", "-qb", "conflict")
	write(t, dir, "a.txt", "conflict\n")
	git(t, dir, "commit", "-qam", "conflicting work")

	git(t, dir, "checkout", "-q", "main")
	git(t, dir, "checkout", "-qb", "merged")
	write(t, dir, "d.txt", "done\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "merged work")
	git(t, dir, "checkout", "-q", "main")
	git(t, dir, "merge", "-q", "--ff-only", "merged")

	write(t, dir, "a.txt", "main\n")
	git(t, dir, "commit", "-qam", "main work")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHead(t *testing.T) {
	dir := gitRepo(t)
	head, err := Head(dir)
	if err != nil || head != "main" {
		t.Fatalf("Head = %q, %v; want main", head, err)
	}
	git(t, dir, "checkout", "-q", "--detach", "HEAD")
	head, err = Head(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(head) != 7 || strings.ContainsAny(head, "/ ") {
		t.Errorf("detached Head = %q; want a short hash", head)
	}
}

func TestAheadBehind(t *testing.T) {
	dir := gitRepo(t)
	ahead, behind, err := AheadBehind(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if ahead != 1 || behind != 2 {
		t.Errorf("ahead, behind = %d, %d; want 1, 2", ahead, behind)
	}
	ahead, behind, err = AheadBehind(dir, "main", "merged")
	if err != nil || ahead != 0 || behind != 1 {
		t.Errorf("merged: %d, %d, %v; want 0, 1", ahead, behind, err)
	}
}

func TestDiffStat(t *testing.T) {
	dir := gitRepo(t)
	files, added, removed, err := DiffStat(dir, "main", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if files != 2 || added != 2 || removed != 1 {
		t.Errorf("files, added, removed = %d, %d, %d; want 2, 2, 1", files, added, removed)
	}
}

func TestDiffStatBinaryCountsAsAFile(t *testing.T) {
	dir := gitRepo(t)
	git(t, dir, "checkout", "-q", "main")
	git(t, dir, "checkout", "-qb", "binary")
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte{0, 1, 2, 3, 4, 5}, 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "binary")
	files, added, removed, err := DiffStat(dir, "main", "binary")
	if err != nil || files != 1 || added != 0 || removed != 0 {
		t.Errorf("files, added, removed = %d, %d, %d, %v; want 1, 0, 0", files, added, removed, err)
	}
}

func TestUncommitted(t *testing.T) {
	dir := gitRepo(t)
	git(t, dir, "checkout", "-q", "main")
	if n, err := Uncommitted(dir); err != nil || n != 0 {
		t.Fatalf("clean Uncommitted = %d, %v", n, err)
	}
	write(t, dir, "a.txt", "changed\n")
	write(t, dir, "new.txt", "new\n")
	if n, err := Uncommitted(dir); err != nil || n != 2 {
		t.Errorf("Uncommitted = %d, %v; want 2", n, err)
	}
}

func TestMergeCheck(t *testing.T) {
	dir := gitRepo(t)
	cases := []struct {
		branch string
		want   string
		files  []string
	}{
		{"feature", Clean, nil},
		{"conflict", Conflicts, []string{"a.txt"}},
		{"merged", Merged, nil},
	}
	for _, c := range cases {
		got, err := MergeCheck(dir, "main", c.branch)
		if err != nil {
			t.Fatalf("%s: %v", c.branch, err)
		}
		if got.Result != c.want {
			t.Errorf("%s: result = %q; want %q", c.branch, got.Result, c.want)
		}
		if !slices.Equal(got.Conflicts, c.files) {
			t.Errorf("%s: conflicts = %v; want %v", c.branch, got.Conflicts, c.files)
		}
	}
}

func TestErrorsSayWhatFailed(t *testing.T) {
	dir := t.TempDir()
	if _, err := Head(dir); err == nil || !strings.HasPrefix(err.Error(), "git ") {
		t.Errorf("Head outside a repository = %v; want a git error", err)
	}
	if _, _, err := AheadBehind(dir, "main", "nope"); err == nil || !strings.HasPrefix(err.Error(), "git ") {
		t.Errorf("AheadBehind = %v; want a git error", err)
	}
}
