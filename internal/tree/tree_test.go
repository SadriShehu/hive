package tree

import (
	"testing"

	"github.com/sadrishehu/hive/internal/store"
)

func TestDropEmpty(t *testing.T) {
	sessions := []store.Session{
		{ID: "claude:work", Title: "real work", Status: store.StatusExited},
		// A pre-warmed spare with another spare recorded under it.
		{ID: "claude:spare", ParentID: "claude:work", Status: store.StatusExited},
		{ID: "claude:spare2", ParentID: "claude:spare", Status: store.StatusExited},
		// An empty session that spawned something real stays, to hold it.
		{ID: "claude:quiet", Status: store.StatusExited},
		{ID: "opencode:job", ParentID: "claude:quiet", Title: "job", Status: store.StatusExited},
		// Running sessions stay even before their first message.
		{ID: "claude:new", PID: 42, Status: store.StatusIdle},
	}
	var ids []string
	Walk(DropEmpty(Build(sessions)), func(n *Node, _ string) { ids = append(ids, n.ID) })
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	for _, id := range []string{"claude:work", "claude:quiet", "opencode:job", "claude:new"} {
		if !got[id] {
			t.Errorf("%s dropped", id)
		}
	}
	for _, id := range []string{"claude:spare", "claude:spare2"} {
		if got[id] {
			t.Errorf("%s kept", id)
		}
	}
}

func TestCostSkipsInternalChildrenOfAReportedClaudeTotal(t *testing.T) {
	roots := Build([]store.Session{
		{ID: "claude:root", Status: store.StatusExited},
		{ID: "claude:root/a1", ParentID: "claude:root", Kind: store.KindInternal, Status: store.StatusExited},
		{ID: "opencode:child", ParentID: "claude:root", Status: store.StatusExited},
		{ID: "claude:live", PID: 7, Status: store.StatusWorking},
		{ID: "claude:live/a2", ParentID: "claude:live", Kind: store.KindInternal, Status: store.StatusWorking},
		{ID: "codex:free", Status: store.StatusExited},
	})
	usages := map[string]store.Usage{
		"claude:root":    {ID: "claude:root", CostUSD: 10, CostSource: store.CostTool},
		"claude:root/a1": {ID: "claude:root/a1", CostUSD: 3, CostSource: store.CostConfig},
		"opencode:child": {ID: "opencode:child", CostUSD: 1, CostSource: store.CostTool},
		"claude:live":    {ID: "claude:live", CostUSD: 2, CostSource: store.CostConfig},
		"claude:live/a2": {ID: "claude:live/a2", CostUSD: 0.5, CostSource: store.CostConfig},
		"codex:free":     {ID: "codex:free"},
	}
	total, estimate := Cost(roots, usages)
	if total != 13.5 || !estimate {
		t.Errorf("Cost = %v, %v; want 13.5 with an estimate", total, estimate)
	}
	delete(usages, "claude:live")
	delete(usages, "claude:live/a2")
	if total, estimate := Cost(roots, usages); total != 11 || estimate {
		t.Errorf("reported only: %v, %v", total, estimate)
	}
}
