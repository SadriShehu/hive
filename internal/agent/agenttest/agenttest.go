// Package agenttest helps test adapters without running the real tools.
package agenttest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FakeTool puts a program called name first on PATH for the rest of the test:
// a shell script that logs its arguments, then runs script, which sees them
// as "$@". It returns the log, one line per run.
func FakeTool(t *testing.T, name, script string) (log func() string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, name+".log")
	body := "#!/bin/sh\necho \"$@\" >> '" + logPath + "'\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() string {
		data, _ := os.ReadFile(logPath)
		return strings.TrimSpace(string(data))
	}
}

// Files creates each file under dir, with its folders, holding its own path.
func Files(t *testing.T, dir string, files ...string) {
	t.Helper()
	for _, f := range files {
		path := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Exists reports which of files still exist under dir.
func Exists(dir string, files ...string) []string {
	var out []string
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			out = append(out, f)
		}
	}
	return out
}
