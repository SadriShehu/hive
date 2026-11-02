package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/fusion"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
	"github.com/sadrishehu/hive/internal/usage"
)

// `hive new <tool> --model <name> --dry-run` prints the model the tool will
// actually get.
func TestDryRunPrintsTheResolvedModel(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tr := tracker.New(st, adapters.Builtin(), &tracker.System{})
	tr.Models = usage.Builtin()

	var out, errOut bytes.Buffer
	if err := printDryRun(context.Background(), tr, tracker.LaunchOptions{Tool: "claude", Model: "opus 5.5"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "claude claude-opus-5-5" {
		t.Fatalf("dry run = %q, want claude claude-opus-5-5", got)
	}

	out.Reset()
	if err := printDryRun(context.Background(), tr, tracker.LaunchOptions{Tool: "claude", Model: "gpt-5-5"}, &out, &errOut); err == nil {
		t.Fatal("a model from another provider was accepted")
	}
	if out.Len() != 0 {
		t.Errorf("printed %q for a refused model", out.String())
	}
}

// A dry run says when a tool was left out because its 5h window is nearly spent.
func TestDryRunSaysAToolWasRankedDown(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tr := tracker.New(st, adapters.Builtin(), &tracker.System{})
	tr.Models = usage.Builtin()
	now := time.Now()
	if err := st.SetQuota(store.Quota{Tool: "claude", FiveUsed: 93, FiveReset: now.Add(time.Hour).Unix(), At: now.Unix()}); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if err := printDryRun(context.Background(), tr, tracker.LaunchOptions{Tool: fusion.Auto, Prompt: "rename the variable"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "claude ranked down: 5h window at 93%, resets ") {
		t.Fatalf("dry run explanation = %q, want the ranked-down reason", errOut.String())
	}
	if strings.HasPrefix(out.String(), "claude ") {
		t.Fatalf("dry run picked %q, want another tool", out.String())
	}
}
