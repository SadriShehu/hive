package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/gitx"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
)

func TestWorktreeTextWordsTheBranch(t *testing.T) {
	st := tracker.WorktreeState{
		Worktree:    store.Worktree{Role: "backend", Branch: "hive/api-v2/backend"},
		Ahead:       3,
		Behind:      1,
		Diff:        tracker.DiffStat{Files: 5, Added: 210, Removed: 34},
		Uncommitted: 2,
		Merge:       gitx.Clean,
	}
	got := worktreeText(st)
	for _, want := range []string{"⎇ hive/api-v2/backend", "↑3 ↓1", "5 files +210 −34", "2 uncommitted", "✓ merges clean"} {
		if !strings.Contains(got, want) {
			t.Errorf("worktree text lacks %q: %q", want, got)
		}
	}

	st.Behind = 0
	st.Merge, st.Conflicts = gitx.Conflicts, []string{"web/app.tsx", "api/x.go"}
	if got := worktreeText(st); !strings.Contains(got, "✗ conflicts: web/app.tsx (+1)") {
		t.Errorf("conflict text = %q", got)
	}

	merged := st
	merged.Ahead, merged.Behind, merged.Diff, merged.Uncommitted = 0, 2, tracker.DiffStat{}, 0
	merged.Merge, merged.Conflicts = gitx.Merged, nil
	if got := worktreeText(merged); !strings.Contains(got, "merged ✓") || strings.Contains(got, "↓") {
		t.Errorf("merged text = %q", got)
	}

	gone := st
	gone.Ahead, gone.Note = -1, "its folder is gone"
	if got := worktreeText(gone); !strings.HasPrefix(got, "⎇ ? ") {
		t.Errorf("a broken worktree = %q", got)
	}
}

func TestWorktreeBoardJSONHasTheBranch(t *testing.T) {
	b := &worktreeBoard{Base: "feat/x", Trees: []tracker.WorktreeState{{
		Worktree:    store.Worktree{Role: "backend", Branch: "hive/api-v2/backend"},
		Ahead:       3,
		Behind:      1,
		Diff:        tracker.DiffStat{Files: 5, Added: 210, Removed: 34},
		Uncommitted: 2,
		Merge:       gitx.Conflicts,
		Conflicts:   []string{"web/app.tsx"},
	}}}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{`"base":"feat/x"`, `"ahead":3`, `"behind":1`, `"files":5`, `"added":210`,
		`"removed":34`, `"uncommitted":2`, `"merge":"conflicts"`, `"conflicts":["web/app.tsx"]`} {
		if !strings.Contains(got, want) {
			t.Errorf("board JSON lacks %s:\n%s", want, got)
		}
	}
}
