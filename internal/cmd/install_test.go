package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStableBinPathPrefersThePathEntryThatLinksToTheSameFile(t *testing.T) {
	resolvedExe := executableFile(t, "hive-real")
	pathDir := t.TempDir()
	link := filepath.Join(pathDir, "hive")
	if err := os.Symlink(resolvedExe, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	if got := stableBinPath(resolvedExe); got != link {
		t.Fatalf("got %q, want %q", got, link)
	}
}

func TestStableBinPathKeepsTheResolvedFileWhenPathHasAnotherHive(t *testing.T) {
	resolvedExe := executableFile(t, "hive-real")
	other := executableFile(t, "hive")
	t.Setenv("PATH", filepath.Dir(other))
	if got := stableBinPath(resolvedExe); got != resolvedExe {
		t.Fatalf("got %q, want %q", got, resolvedExe)
	}
}

func TestStableBinPathKeepsTheResolvedFileWithoutHiveOnPath(t *testing.T) {
	resolvedExe := executableFile(t, "hive-real")
	t.Setenv("PATH", t.TempDir())
	if got := stableBinPath(resolvedExe); got != resolvedExe {
		t.Fatalf("got %q, want %q", got, resolvedExe)
	}
}

func executableFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
