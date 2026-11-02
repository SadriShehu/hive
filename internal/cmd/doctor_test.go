package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func captureStdout(t *testing.T, run func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	run()
	w.Close()
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	return string(out)
}

func TestLogWritesTheLevelAfterTheTimestamp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HIVE_HOME", dir)
	logInfo("team deliver %s: sent", "claude:A")
	logf("hook claude: boom")
	b, err := os.ReadFile(filepath.Join(dir, "hive.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{" INFO [", "] team deliver claude:A: sent", " ERROR [", "] hook claude: boom"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("log = %q, want %q", b, want)
		}
	}
}

func TestCheckLogCountsOnlyErrors(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HIVE_HOME", dir)
	at := func(d time.Duration) string { return time.Now().Add(d).Format(time.RFC3339) }
	lines := []string{
		at(-6*time.Hour) + " [7] hook claude: old failure one",
		at(-5*time.Hour) + " INFO [7] team deliver claude:A: sent",
		at(-4*time.Hour) + " [7] team deliver claude:B: sent",
		at(-3*time.Hour) + " [7] team deliver claude:C: busy again; the next idle delivers",
		at(-2*time.Hour) + " ERROR [7] hook claude: error two",
		at(-1*time.Hour) + " [7] hook codex: old failure three",
		at(-10*time.Minute) + " ERROR [7] hook opencode: error four",
		at(-26*time.Hour) + " ERROR [7] hook claude: yesterday",
	}
	if err := os.WriteFile(filepath.Join(dir, "hive.log"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var c checkup
	out := captureStdout(t, func() { checkLog(&c) })
	if !strings.Contains(out, "4 errors in the last day") {
		t.Errorf("checkLog = %q, want four errors", out)
	}
	for _, want := range []string{"error two", "old failure three", "error four"} {
		if !strings.Contains(out, want) {
			t.Errorf("checkLog = %q, want the last three to include %q", out, want)
		}
	}
	for _, gone := range []string{"old failure one", "yesterday", "team deliver"} {
		if strings.Contains(out, gone) {
			t.Errorf("checkLog = %q, want no %q", out, gone)
		}
	}
	if c.problems != 0 {
		t.Errorf("log errors counted as %d problems, want none", c.problems)
	}
}
