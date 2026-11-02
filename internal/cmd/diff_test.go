package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
)

func diffRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		diffGit(t, dir, args...)
	}
	diffWrite(t, dir, "a.txt", "one\n")
	diffGit(t, dir, "add", ".")
	diffGit(t, dir, "commit", "-qm", "base")
	diffWrite(t, dir, "a.txt", "one\ntwo\n")
	diffWrite(t, dir, "b.txt", "new\n")
	return dir
}

func diffGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func diffWrite(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// diffSession puts one session, in folder, in a fresh store and returns its ID.
func diffSession(t *testing.T, folder string) string {
	t.Helper()
	t.Setenv("HIVE_HOME", t.TempDir())
	st, err := store.Open(paths.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := store.Session{ID: "claude:T", Tool: "claude", NativeID: "T", Cwd: folder,
		Status: store.StatusIdle, Source: store.SourceHook, CreatedAt: 1, UpdatedAt: 1}
	if err := st.Upsert(s); err != nil {
		t.Fatal(err)
	}
	return s.ID
}

func TestDiffStatPrintsTheSummaryAndFiles(t *testing.T) {
	repo := diffRepo(t)
	id := diffSession(t, repo)
	root := newRootCmd()
	root.SetArgs([]string{"diff", id, "--stat"})
	var runErr error
	out := captureStdout(t, func() { runErr = root.Execute() })
	if runErr != nil {
		t.Fatalf("hive diff --stat: %v", runErr)
	}
	for _, want := range []string{"changes in this folder", "2 files", "+1 −0", "2 uncommitted",
		"M a.txt", "? b.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("stat output lacks %q:\n%s", want, out)
		}
	}
}

func TestDiffJSONHasTheFilesAndCounts(t *testing.T) {
	repo := diffRepo(t)
	id := diffSession(t, repo)
	root := newRootCmd()
	root.SetArgs([]string{"diff", id, "--json"})
	var runErr error
	out := captureStdout(t, func() { runErr = root.Execute() })
	if runErr != nil {
		t.Fatalf("hive diff --json: %v", runErr)
	}
	var got struct {
		Label string `json:"label"`
		Files []struct {
			Path   string `json:"path"`
			Status string `json:"status"`
			Added  int    `json:"added"`
		} `json:"files"`
		Uncommitted int `json:"uncommitted"`
		Ahead       int `json:"ahead"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("JSON: %v\n%s", err, out)
	}
	if got.Label != "changes in this folder" || got.Uncommitted != 2 || got.Ahead != 0 {
		t.Errorf("JSON = %+v", got)
	}
	if len(got.Files) != 2 {
		t.Fatalf("files = %+v, want a.txt and b.txt", got.Files)
	}
	found := false
	for _, f := range got.Files {
		if f.Path == "a.txt" && f.Status == "M" && f.Added == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("JSON lacks a.txt as M +1: %+v", got.Files)
	}
}

func TestDiffRefusesAFolderThatIsNotARepository(t *testing.T) {
	id := diffSession(t, t.TempDir())
	root := newRootCmd()
	root.SetArgs([]string{"diff", id, "--stat"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("hive diff outside a repository = %v", err)
	}
}
