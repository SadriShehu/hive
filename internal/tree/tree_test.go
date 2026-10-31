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

func TestBuildTeamsGivesATeamANodeWithItsMembersFlatUnderIt(t *testing.T) {
	sessions := []store.Session{
		{ID: "claude:boss", Title: "boss", PID: 1, Status: store.StatusIdle, UpdatedAt: 10},
		{ID: "claude:L", Title: "lead", ParentID: "claude:boss", PID: 2, Status: store.StatusWorking, UpdatedAt: 20},
		{ID: "codex:F", Title: "frontend", ParentID: "claude:boss", PID: 3, Status: store.StatusAttention, UpdatedAt: 30},
		{ID: "claude:sub", Title: "a subagent", ParentID: "claude:L", Kind: store.KindInternal, PID: 0, Status: store.StatusWorking},
		{ID: "claude:other", Title: "not on a team", PID: 4, Status: store.StatusIdle},
		{ID: "opencode:pid-9", Pane: "%9", Source: "pending", PID: 9},
	}
	teams := []store.Team{
		{ID: "t1", Name: "api", CreatedBy: "claude:boss", CreatedAt: 5, UpdatedAt: 5},
		{ID: "t2", Name: "empty", CreatedAt: 6, UpdatedAt: 6},
	}
	members := []store.Member{
		{TeamID: "t1", Role: "lead", SessionID: "claude:L"},
		{TeamID: "t1", Role: "frontend", SessionID: "codex:F"},
		{TeamID: "t1", Role: "reviewer", Pane: "%9"}, // not reported in: its pending session stands in
		{TeamID: "t1", Role: "gone", SessionID: "claude:other", LeftAt: 1},
	}
	roots := BuildTeams(sessions, teams, members)

	var team, boss *Node
	for _, r := range roots {
		switch r.ID {
		case "claude:boss":
			boss = r
		case TeamPrefix + "t2":
			if r.Team == nil || r.Team.Name != "empty" || len(r.Children) != 0 {
				t.Errorf("a team with no members yet = %+v", r)
			}
		}
	}
	if boss == nil || len(boss.Children) != 1 || boss.Children[0].Team == nil {
		t.Fatalf("the team should hang under the agent that made it: %+v", boss)
	}
	team = boss.Children[0]
	if team.Status != store.StatusAttention || !team.Live {
		t.Errorf("team status = %s live=%v, want attention and live (a member needs the user)", team.Status, team.Live)
	}
	var kids []string
	for _, c := range team.Children {
		kids = append(kids, c.Role+"="+c.ID)
		if c.TeamID != "t1" {
			t.Errorf("member %s has TeamID %q", c.ID, c.TeamID)
		}
	}
	if got := len(team.Children); got != 3 {
		t.Fatalf("members under the team = %v, want lead, frontend and the pending reviewer", kids)
	}
	var lead *Node
	for _, c := range team.Children {
		if c.Role == "lead" {
			lead = c
		}
	}
	if lead == nil || len(lead.Children) != 1 || lead.Children[0].ID != "claude:sub" {
		t.Errorf("a member keeps its own subagents under it: %+v", lead)
	}
	// A retired member goes back to being a plain session.
	for _, r := range roots {
		if r.ID == "claude:other" && r.Role != "" {
			t.Errorf("a retired member is still shown as %q", r.Role)
		}
	}
}
