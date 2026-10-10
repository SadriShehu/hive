package fusion

import (
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/usage"
)

func TestAssessReadsTheKindOfWorkAndHowHardItIs(t *testing.T) {
	tests := []struct {
		prompt string
		want   string
	}{
		{"fix the flaky test in store", "debugging 6"},
		{"just fix the typo in the error", "debugging 4"},
		{"the sync crashes with a nil pointer when the database is locked; find the root cause and fix it",
			"debugging 6"},
		{"design the migration of every session to the new schema, carefully, with a plan for the rollback",
			"reasoning 8, coding 8"},
		{"write tests for the parser", "coding 6"},
		{"add a --json flag to hive trash", "coding 6"},
		{"run the tests and push", "tool_use 6"},
		{"rename the variable", "coding 4"},
		{"make it faster", "coding 6"},
		{"", "reasoning 8, coding 8, debugging 8, tool_use 8"},
	}
	for _, tt := range tests {
		if got := Assess(tt.prompt).String(); got != tt.want {
			t.Errorf("Assess(%q) = %q, want %q", tt.prompt, got, tt.want)
		}
	}
}

func TestAssessRaisesTheLevelForLongPromptsWithManySteps(t *testing.T) {
	prompt := "implement the admin page:\n1. add the handler\n2. add the template\n3. add the tests\n4. wire the route"
	need := Assess(prompt)
	if need.Levels[usage.Coding] != 8 {
		t.Errorf("four steps = %v (%v)", need.Levels, need.Cues)
	}
	words := strings.Repeat("add a field to the form and show it in the list ", 20)
	if got := Assess(words).Levels[usage.Coding]; got != 8 {
		t.Errorf("200 words = %d", got)
	}
	if !strings.Contains(strings.Join(need.Cues, "; "), "steps") {
		t.Errorf("cues don't say why: %v", need.Cues)
	}
}

func catalog() usage.Catalog {
	return usage.NewCatalog(usage.Builtin(), []usage.Model{
		{Name: "gpt-6-luna", Tier: usage.TierStrong, Input: 2, Output: 8},
		{Name: "unrated-1"},
	})
}

func candidates(t *testing.T, pairs ...string) []Candidate {
	t.Helper()
	c := catalog()
	var out []Candidate
	for _, p := range pairs {
		tool, id, _ := strings.Cut(p, " ")
		m, ok := c.Match(id)
		if !ok {
			t.Fatalf("no model %q", id)
		}
		out = append(out, Candidate{Tool: tool, ID: id, Model: m})
	}
	return out
}

func TestPickTakesTheLightestModelThatMeetsTheBar(t *testing.T) {
	all := candidates(t, "claude claude-fable-5-1", "claude claude-opus-5-5", "claude claude-sonnet-5-5",
		"claude claude-haiku-4-5", "codex gpt-6-luna", "copilot claude-opus-4.7")
	for _, tt := range []struct {
		prompt, want string
		met          bool
	}{
		{"rename the variable", "claude · claude-haiku-4-5", true},
		{"add a --json flag to hive trash", "claude · claude-sonnet-5-5", true},
		{"design the architecture of the sync", "codex · gpt-6-luna", true}, // strong, and cheaper than opus
		{"prove the migration keeps every row, carefully, with the plan for it to go ahead and the rollback and the whole set of invariants listed one by one, which is a lot to do:\n1. list them\n2. check them\n3. write the plan",
			"claude · claude-fable-5-1", true},
	} {
		choice, err := Pick(ModeProvider, "", all, Assess(tt.prompt))
		if err != nil {
			t.Fatal(err)
		}
		if choice.String() != tt.want || choice.Met != tt.met {
			t.Errorf("Pick(%q) = %s (met %v): %s", tt.prompt, choice, choice.Met, choice.Reason)
		}
	}
}

func TestPickFallsBackToTheClosestWhenNothingMeetsTheBar(t *testing.T) {
	few := candidates(t, "claude claude-haiku-4-5", "claude claude-sonnet-5-5")
	choice, err := Pick(ModeModel, "claude", few, Assess("design the architecture of the whole sync"))
	if err != nil {
		t.Fatal(err)
	}
	if choice.Met || choice.ID != "claude-sonnet-5-5" || !strings.Contains(choice.Reason, "closest") {
		t.Errorf("fallback = %s (met %v): %s", choice, choice.Met, choice.Reason)
	}
}

func TestPickPrefersTheNamedToolAmongEquals(t *testing.T) {
	both := candidates(t, "claude claude-opus-5-5", "copilot claude-opus-5-5")
	for prefer, want := range map[string]string{"copilot": "copilot", "claude": "claude", "": "claude"} {
		choice, err := Pick(ModeProvider, prefer, both, Assess("review the security of the handlers"))
		if err != nil {
			t.Fatal(err)
		}
		if choice.Tool != want {
			t.Errorf("prefer %q: picked %s", prefer, choice)
		}
	}
}

func TestPickNeedsCandidates(t *testing.T) {
	if _, err := Pick(ModeProvider, "", nil, Assess("x")); err == nil {
		t.Error("no error without candidates")
	}
}

func TestParseMode(t *testing.T) {
	if m, ok := ParseMode(" Provider "); !ok || m != ModeProvider {
		t.Errorf("ParseMode = %q, %v", m, ok)
	}
	if _, ok := ParseMode("both"); ok {
		t.Error("an unknown mode parsed")
	}
}
