package tracker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/fusion"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/usage"
)

// fakeTool is an adapter with only a spec; listingTool also lists models.
type fakeTool struct{ spec agent.Spec }

func (f fakeTool) Spec() agent.Spec { return f.spec }
func (f fakeTool) ParseHook([]byte, []string, func(string) string) ([]agent.Event, error) {
	return nil, nil
}
func (f fakeTool) Install(string) (string, error) { return "", nil }
func (f fakeTool) Uninstall() (string, error)     { return "", nil }
func (f fakeTool) Installed() bool                { return true }

type listingTool struct {
	fakeTool
	models []string
	err    error
}

func (l listingTool) ListModels(context.Context) ([]string, error) { return l.models, l.err }

// modelWorld is a machine with three fake tools on PATH: claude, which takes
// a model and lists some; opencode, which lists its own; and plain, whose
// start command takes no model. History has claude on opus and codex on
// gpt-6-luna, though codex isn't installed.
func modelWorld(t *testing.T) *Tracker {
	t.Helper()
	bin := t.TempDir()
	for _, name := range []string{"claude", "opencode", "plain"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for id, model := range map[string]string{"claude:A": "claude-opus-5-5", "claude:B": "claude-fable-5-1", "codex:C": "gpt-6-luna"} {
		tool, native, _ := strings.Cut(id, ":")
		if err := errors.Join(st.Upsert(store.Session{ID: id, Tool: tool, NativeID: native}),
			st.UpsertUsage(store.UsageRecord{Usage: store.Usage{ID: id, Model: model}, Version: 1})); err != nil {
			t.Fatal(err)
		}
	}
	adapters := []agent.Adapter{
		fakeTool{agent.Spec{Name: "claude", New: []string{"claude", "--model", "{model}", "{prompt}"},
			Models: []string{"claude-fable-5-1", "claude-sonnet-5-5", "claude-haiku-4-5", "claude-opus-4.6-fast"}}},
		listingTool{fakeTool: fakeTool{agent.Spec{Name: "opencode", New: []string{"opencode", "--model", "{model}", "--prompt", "{prompt}"}}},
			models: []string{"deepseek/deepseek-v4-pro", "google-vertex/claude-opus-5-5@default", "google-vertex-anthropic/claude-opus-5-5@default", "opencode/mystery-free"}},
		fakeTool{agent.Spec{Name: "plain", New: []string{"plain", "{prompt}"}, Models: []string{"gpt-6-pro"}}},
		fakeTool{agent.Spec{Name: "codex", New: []string{"codex", "--model", "{model}", "{prompt}"}, Models: []string{"gpt-6-pro"}}},
	}
	tr := New(st, adapters, &fakeWorld{env: map[string]string{}})
	tr.Models = usage.Builtin()
	return tr
}

func TestToolModelsMergeTheSpecTheToolAndHistory(t *testing.T) {
	tr := modelWorld(t)
	ctx := context.Background()
	claude, err := tr.ToolModels(ctx, "claude")
	if err != nil || strings.Join(claude, " ") != "claude-fable-5-1 claude-sonnet-5-5 claude-haiku-4-5 claude-opus-4.6-fast claude-opus-5-5" {
		t.Errorf("claude = %v, %v: want the spec's list then history, without repeats", claude, err)
	}
	opencode, err := tr.ToolModels(ctx, "opencode")
	if err != nil || len(opencode) != 4 || opencode[0] != "deepseek/deepseek-v4-pro" {
		t.Errorf("opencode = %v, %v: want what it lists", opencode, err)
	}
	if _, err := tr.ToolModels(ctx, "nope"); err == nil {
		t.Error("an unknown tool listed models")
	}
}

func TestCandidatesKeepRatedModelsOnce(t *testing.T) {
	tr := modelWorld(t)
	got, err := tr.Candidates(context.Background(), []string{"claude", "opencode", "plain"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range got {
		names = append(names, c.String())
	}
	want := "claude · claude-fable-5-1, claude · claude-sonnet-5-5, claude · claude-haiku-4-5, claude · claude-opus-5-5, " +
		"opencode · deepseek/deepseek-v4-pro, opencode · google-vertex/claude-opus-5-5@default"
	if strings.Join(names, ", ") != want {
		t.Errorf("candidates = %s\nwant %s", strings.Join(names, ", "), want)
	}
}

func TestPickAcrossToolsAndInsideOne(t *testing.T) {
	tr := modelWorld(t)
	ctx := context.Background()

	across, err := tr.Pick(ctx, PickOptions{Tool: fusion.Auto, Prompt: "rename the variable"})
	if err != nil || across.String() != "claude · claude-haiku-4-5" || across.Mode != fusion.ModeProvider {
		t.Errorf("provider mode = %v, %v", across, err)
	}

	inside, err := tr.Pick(ctx, PickOptions{Tool: "opencode", Model: fusion.Auto, Prompt: "rename the variable"})
	if err != nil || inside.String() != "opencode · deepseek/deepseek-v4-pro" || inside.Mode != fusion.ModeModel || !inside.Met {
		t.Errorf("model mode = %v (met %v), %v: a strong model meets a light need, and the first of equals wins", inside, inside.Met, err)
	}

	tr.Fusion = fusion.Settings{Mode: fusion.ModeModel, Tool: "opencode"}
	configured, err := tr.Pick(ctx, PickOptions{Tool: fusion.Auto, Prompt: "design the whole sync"})
	if err != nil || configured.String() != "opencode · deepseek/deepseek-v4-pro" {
		t.Errorf("configured model mode = %v, %v", configured, err)
	}
	overridden, err := tr.Pick(ctx, PickOptions{Tool: fusion.Auto, Mode: fusion.ModeProvider, Prompt: "design the whole sync"})
	if err != nil || overridden.Tool != "opencode" || overridden.Mode != fusion.ModeProvider {
		t.Errorf("--mode provider with the configured tool preferred = %v, %v", overridden, err)
	}
}

func TestPickPrefersTheParentsToolAndNeedsAModelFlag(t *testing.T) {
	tr := modelWorld(t)
	ctx := context.Background()
	if err := tr.Store.Upsert(store.Session{ID: "opencode:P", Tool: "opencode", NativeID: "P"}); err != nil {
		t.Fatal(err)
	}
	child, err := tr.Pick(ctx, PickOptions{Tool: fusion.Auto, Prompt: "review the security of the handlers", ParentID: "opencode:P"})
	if err != nil || child.Tool != "opencode" || child.Model.Tier != usage.TierStrong {
		t.Errorf("under an opencode parent = %v, %v: want a strong model from the parent's tool", child, err)
	}
	if _, err := tr.Pick(ctx, PickOptions{Tool: "plain", Model: fusion.Auto}); err == nil || !strings.Contains(err.Error(), "{model}") {
		t.Errorf("a tool without {model}: %v", err)
	}
	if _, err := tr.Pick(ctx, PickOptions{Tool: "nope", Model: fusion.Auto}); err == nil {
		t.Error("an unknown tool picked a model")
	}
	tr.Fusion = fusion.Settings{Mode: fusion.ModeModel}
	fromParent, err := tr.Pick(ctx, PickOptions{Tool: fusion.Auto, Prompt: "rename the variable", ParentID: "opencode:P"})
	if err != nil || fromParent.Tool != "opencode" {
		t.Errorf("model mode without a configured tool takes the parent's: %v, %v", fromParent, err)
	}
}
