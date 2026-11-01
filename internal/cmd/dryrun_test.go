package cmd

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/adapters"
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
