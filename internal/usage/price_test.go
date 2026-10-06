package usage

import (
	"math"
	"testing"

	"github.com/sadrishehu/hive/internal/store"
)

func close(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCostSplitsCacheWritesByTTL(t *testing.T) {
	m := anthropic("claude-fable-5-1", TierFrontier, 10, 50, 0.25)
	tokens := store.Tokens{Input: 1e6, Output: 1e6, CacheRead: 1e6, CacheWrite: 2e6, CacheWrite1h: 1e6}
	if got := m.Cost(tokens); !close(got, 10+50+0.25+12.5+20) {
		t.Errorf("cost = %v", got)
	}
	flat := Model{Name: "x", Input: 1, Output: 2, CacheRead: 0.1, CacheWrite: 1.5}
	if got := flat.Cost(store.Tokens{CacheWrite: 2e6, CacheWrite1h: 1e6}); !close(got, 3) {
		t.Errorf("a model without a 1h price charges every write at the 5m price: %v", got)
	}
}

func TestLookupNormalizesIDs(t *testing.T) {
	c := Builtin()
	for id, want := range map[string]string{
		"claude-opus-5-5[1m]":             "claude-opus-5-5",
		"claude-haiku-4-5-20251001":       "claude-haiku-4-5",
		"anthropic/claude-sonnet-5-5":     "claude-sonnet-5-5",
		"Claude-Fable-5-1":                "claude-fable-5-1",
		"claude-sonnet-4-20250514":        "claude-sonnet-4",
		"claude-opus-4-6-some-new-suffix": "claude-opus-4-6",
	} {
		if m, ok := c.Lookup(id); !ok || m.Name != want {
			t.Errorf("Lookup(%q) = %q, %v; want %q", id, m.Name, ok, want)
		}
	}
	if _, ok := c.Lookup("mystery-9"); ok {
		t.Error("an unknown model was found")
	}
}

func TestApplyPricesOnlyWhenEveryModelIsKnown(t *testing.T) {
	c := Builtin()
	fable := store.Tokens{Input: 1e6, Output: 1e6}
	u := store.Usage{Models: map[string]store.Tokens{"claude-fable-5-1": fable, "gpt-6-luna": {Input: 1}}}
	if got := c.Apply(u); got.CostSource != "" || got.CostUSD != 0 {
		t.Errorf("a half-known mix was priced: %+v", got)
	}
	u.Models = map[string]store.Tokens{"claude-fable-5-1": fable}
	if got := c.Apply(u); got.CostSource != store.CostConfig || !close(got.CostUSD, 60) {
		t.Errorf("estimate = %+v", got)
	}
	reported := store.Usage{CostUSD: 1.25, CostSource: store.CostTool, Models: u.Models}
	if got := c.Apply(reported); got.CostUSD != 1.25 || got.CostSource != store.CostTool {
		t.Errorf("a reported cost was replaced: %+v", got)
	}
	withConfig := NewCatalog(c, []Model{{Name: "gpt-6-luna", Input: 1, Output: 2}})
	u.Models = map[string]store.Tokens{"gpt-6-luna": {Input: 1e6, Output: 1e6}}
	if got := withConfig.Apply(u); got.CostSource != store.CostConfig || !close(got.CostUSD, 3) {
		t.Errorf("config price = %+v", got)
	}
}

func TestApplyWindowFromConfigWinsOverTheTool(t *testing.T) {
	c := NewCatalog(Builtin(), []Model{{Name: "gpt-6-luna", ContextWindow: 258_400}})
	if got := c.Apply(store.Usage{Model: "gpt-6-luna"}); got.ContextWindow != 258_400 {
		t.Errorf("window = %d", got.ContextWindow)
	}
	if got := c.Apply(store.Usage{Model: "gpt-6-luna", ContextWindow: 100}); got.ContextWindow != 258_400 {
		t.Errorf("a configured window should win: %d", got.ContextWindow)
	}
	if got := c.Apply(store.Usage{Model: "claude-fable-5-1", ContextWindow: 200_000}); got.ContextWindow != 200_000 {
		t.Errorf("a built-in without a window changed the tool's: %d", got.ContextWindow)
	}
}

func TestValidate(t *testing.T) {
	for name, models := range map[string][]Model{
		"no name":   {{Input: 1}},
		"duplicate": {{Name: "a"}, {Name: "A"}},
		"negative":  {{Name: "a", Output: -1}},
	} {
		if Validate(models) == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if err := Validate([]Model{{Name: "a", Input: 1}, {Name: "b"}}); err != nil {
		t.Error(err)
	}
}

func TestBuiltinRowsArePriced(t *testing.T) {
	for _, m := range anthropicModels {
		if !m.Priced() || m.CacheRead <= 0 || m.CacheWrite <= m.Input || m.CacheWrite1h <= m.CacheWrite {
			t.Errorf("%s = %+v", m.Name, m)
		}
	}
}

func TestMatchTakesToolSpellings(t *testing.T) {
	c := Builtin()
	for id, want := range map[string]string{
		"google-vertex-anthropic/claude-opus-5-5@default": "claude-opus-5-5",
		"google-vertex/claude-haiku-4-5@20251001":         "claude-haiku-4-5",
		"claude-opus-4.6":            "claude-opus-4-6",
		"gpt-5.5":                    "gpt-5-5",
		"gpt-6-luna":                 "gpt-6-luna",
		"deepseek/deepseek-v4-pro":   "deepseek-v4-pro",
		"google-vertex/xai/grok-4.6": "grok-4-6",
	} {
		if m, ok := c.Match(id); !ok || m.Name != want {
			t.Errorf("Match(%q) = %q, %v; want %q", id, m.Name, ok, want)
		}
	}
	for _, id := range []string{"claude-opus-4.6-fast", "gemini-2.5-flash-image", "mystery-9"} {
		if m, ok := c.Match(id); ok {
			t.Errorf("Match(%q) = %q; a variant hive doesn't know must stay unrated", id, m.Name)
		}
	}
}

func TestRatingsComeFromTheTierUnlessSet(t *testing.T) {
	m := Model{Name: "x", Tier: TierStrong, Debugging: 10, Speed: 2}
	if m.Rating(Reasoning) != 8 || m.Rating(Debugging) != 10 || m.SpeedRating() != 2 || m.Power() != 34 {
		t.Errorf("ratings = %d %d %d %d", m.Rating(Reasoning), m.Rating(Debugging), m.SpeedRating(), m.Power())
	}
	if (Model{Name: "y"}).Rated() || !(Model{Name: "z", Coding: 5}).Rated() {
		t.Error("a model is rated once it has a tier or a rating")
	}
	if got := (Model{Name: "w", Tier: TierFast}).Rating(ToolUse); got != 4 {
		t.Errorf("fast tier rating = %d", got)
	}
}

func TestValidateRatings(t *testing.T) {
	if err := Validate([]Model{{Name: "x", Tier: "huge"}}); err == nil {
		t.Error("an unknown tier passed")
	}
	if err := Validate([]Model{{Name: "x", Coding: 11}}); err == nil {
		t.Error("a rating over 10 passed")
	}
	if err := Validate([]Model{{Name: "x", Tier: TierFast, Coding: 7, Speed: 9}}); err != nil {
		t.Error(err)
	}
}

func TestBuiltinRowsAreRated(t *testing.T) {
	for _, m := range Builtin().All() {
		if !m.Rated() {
			t.Errorf("%s has no tier", m.Name)
		}
	}
}
