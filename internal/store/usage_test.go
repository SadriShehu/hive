package store

import (
	"reflect"
	"testing"
)

func sampleUsage() Usage {
	return Usage{
		ID:    "claude:A",
		Model: "claude-fable-5-1",
		Models: map[string]Tokens{
			"claude-fable-5-1": {Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, CacheWrite1h: 1, Reasoning: 1},
		},
		Tokens:          Tokens{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, CacheWrite1h: 1, Reasoning: 1},
		CostUSD:         1.5,
		CostSource:      CostTool,
		ContextTokens:   10,
		ContextWindow:   200_000,
		Tools:           map[string]int{"Bash": 2, "Read": 1},
		Skills:          map[string]int{"review": 1},
		Requests:        3,
		Compactions:     1,
		Effort:          "xhigh",
		APIDurationMS:   5,
		LinesAdded:      6,
		LinesRemoved:    7,
		PremiumRequests: 1.5,
		Partial:         true,
		UpdatedAt:       9,
	}
}

func TestUsageRoundTrip(t *testing.T) {
	st := open(t)
	u := sampleUsage()
	must(t, st.UpsertUsage(UsageRecord{Usage: u, Cursor: "c1", Version: 1}))
	got, ok, err := st.UsageRecord("claude:A")
	must(t, err)
	if !ok || !reflect.DeepEqual(got.Usage, u) || got.Cursor != "c1" || got.Version != 1 {
		t.Fatalf("record = %+v, want %+v", got, u)
	}

	u.Requests, u.Partial = 4, false
	must(t, st.UpsertUsage(UsageRecord{Usage: u, Cursor: "c2", Version: 1}))
	got, _, _ = st.UsageRecord("claude:A")
	if got.Requests != 4 || got.Partial || got.Cursor != "c2" {
		t.Errorf("after update = %+v", got)
	}

	all, err := st.AllUsage()
	must(t, err)
	if len(all) != 1 || all["claude:A"].Requests != 4 {
		t.Errorf("AllUsage = %+v", all)
	}
	must(t, st.DeleteUsage("claude:A"))
	if _, ok, _ := st.Usage("claude:A"); ok {
		t.Error("usage survived DeleteUsage")
	}
}

func TestUsageWithoutMapsScansAsEmptyMaps(t *testing.T) {
	st := open(t)
	must(t, st.UpsertUsage(UsageRecord{Usage: Usage{ID: "codex:B", Requests: 1}}))
	got, _, _ := st.Usage("codex:B")
	if got.Models == nil || got.Tools == nil || got.Skills == nil || len(got.Tools) != 0 {
		t.Errorf("maps = %v %v %v", got.Models, got.Tools, got.Skills)
	}
}

func TestResetUsageAndPurgeDropRows(t *testing.T) {
	st := open(t)
	must(t, st.Upsert(Session{ID: "claude:A", Tool: "claude", NativeID: "A", CreatedAt: 1, UpdatedAt: 1}))
	must(t, st.UpsertUsage(UsageRecord{Usage: sampleUsage()}))
	must(t, st.UpsertUsage(UsageRecord{Usage: Usage{ID: "codex:B", Requests: 1}}))
	must(t, st.Purge("claude:A", 5))
	if _, ok, _ := st.Usage("claude:A"); ok {
		t.Error("usage survived Purge")
	}
	must(t, st.ResetUsage())
	if all, _ := st.AllUsage(); len(all) != 0 {
		t.Errorf("AllUsage after reset = %+v", all)
	}
}

func TestUsageEmpty(t *testing.T) {
	if !(Usage{ID: "x", ContextWindow: 200_000}).Empty() {
		t.Error("a row with only a window counts as empty")
	}
	if (Usage{Tools: map[string]int{"Bash": 1}}).Empty() {
		t.Error("a row with tool calls is not empty")
	}
}

func TestModelsSeenGroupsByToolMostUsedFirst(t *testing.T) {
	st := open(t)
	for _, s := range []Session{
		{ID: "claude:A", Tool: "claude", NativeID: "A"}, {ID: "claude:B", Tool: "claude", NativeID: "B"},
		{ID: "claude:C", Tool: "claude", NativeID: "C"}, {ID: "codex:D", Tool: "codex", NativeID: "D"},
		{ID: "codex:E", Tool: "codex", NativeID: "E"},
	} {
		must(t, st.Upsert(s))
	}
	for id, model := range map[string]string{"claude:A": "claude-opus-5-5", "claude:B": "claude-fable-5-1",
		"claude:C": "claude-opus-5-5", "codex:D": "gpt-6-luna", "codex:E": ""} {
		must(t, st.UpsertUsage(UsageRecord{Usage: Usage{ID: id, Model: model}, Version: 1}))
	}
	seen, err := st.ModelsSeen()
	must(t, err)
	want := map[string][]string{"claude": {"claude-opus-5-5", "claude-fable-5-1"}, "codex": {"gpt-6-luna"}}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("ModelsSeen = %v, want %v", seen, want)
	}
}
