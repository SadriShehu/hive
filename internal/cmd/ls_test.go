package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
	"github.com/sadrishehu/hive/internal/tree"
)

// teamTree is a team with two live members whose sessions share a title.
func teamTree(t *testing.T, status string) ([]*tree.Node, *teamData, []store.Session) {
	t.Helper()
	now := time.Now().UnixMilli()
	tm := store.Team{ID: "t1", Name: "api", Goal: "Build the admin API", Status: status, CreatedAt: now - 2, UpdatedAt: now}
	sessions := []store.Session{
		{ID: "claude:aaaa1111-xxxx", Tool: "claude", NativeID: "aaaa1111-xxxx", Title: "Same title", Status: store.StatusWorking, PID: 11, CreatedAt: now - 2, UpdatedAt: now},
		{ID: "claude:bbbb2222-yyyy", Tool: "claude", NativeID: "bbbb2222-yyyy", Title: "Same title", Status: store.StatusIdle, PID: 12, CreatedAt: now - 1, UpdatedAt: now},
	}
	members := []store.Member{
		{TeamID: tm.ID, Role: "lead", SessionID: sessions[0].ID, Tool: "claude", JoinedAt: 1},
		{TeamID: tm.ID, Role: "backend", SessionID: sessions[1].ID, Tool: "claude", JoinedAt: 2},
	}
	data := &teamData{teams: []store.Team{tm}, members: members, tasks: map[string][]store.Task{}}
	return tree.BuildTeams(sessions, data.teams, members), data, sessions
}

func TestLsShowsBothRolesWhenMembersShareATitle(t *testing.T) {
	roots, data, sessions := teamTree(t, store.TeamActive)
	teams, rest := splitTeams(roots)
	out := captureStdout(t, func() {
		printTree(append(teams, rest...), nil, data, shortIDs(sessions), func(int) string { return "" })
	})
	for _, want := range []string{"⬡ api", "2 members", "Build the admin API", "lead · ", "backend · ", "claude:aaaa1111", "claude:bbbb2222"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls = %q, want %q", out, want)
		}
	}
}

func TestLsCollapsesADoneTeamToOneDimLine(t *testing.T) {
	roots, data, sessions := teamTree(t, store.TeamDone)
	teams, rest := splitTeams(roots)
	teams = collapseDoneTeams(teams)
	out := captureStdout(t, func() {
		printTree(append(teams, rest...), nil, data, shortIDs(sessions), func(int) string { return "" })
	})
	if !strings.Contains(out, "⬡ api") || !strings.Contains(out, "done · 2 members") {
		t.Errorf("ls = %q, want the done team on one line", out)
	}
	for _, gone := range []string{"lead · ", "backend · "} {
		if strings.Contains(out, gone) {
			t.Errorf("ls = %q, want no members under a done team without --all", out)
		}
	}
}

func TestAShortIDResolvesAsAPrefix(t *testing.T) {
	s := store.Session{ID: "claude:1043899d-ee11-4a11", Tool: "claude", NativeID: "1043899d-ee11-4a11"}
	got, err := tracker.Lookup([]store.Session{s}, shortIDs([]store.Session{s})[s.ID])
	if err != nil || got.ID != s.ID {
		t.Fatalf("Lookup = %q, %v; want the full session", got.ID, err)
	}
}

func TestShortIDsTakeTheShortestUniquePrefix(t *testing.T) {
	first := store.Session{ID: "opencode:ses_13cced0d1111", Tool: "opencode", NativeID: "ses_13cced0d1111"}
	second := store.Session{ID: "opencode:ses_13ccec542222", Tool: "opencode", NativeID: "ses_13ccec542222"}
	parent := store.Session{ID: "claude:1935c819", Tool: "claude", NativeID: "1935c819"}
	child := store.Session{ID: "claude:1935c819/worker", Tool: "claude", NativeID: "1935c819/worker"}
	sessions := []store.Session{first, second, parent, child}
	ids := shortIDs(sessions)
	for _, s := range sessions {
		short, ok := ids[s.ID]
		if !ok || !strings.HasPrefix(s.ID, short) {
			t.Errorf("short ID for %q = %q, %v; want a prefix", s.ID, short, ok)
		}
	}
	if ids[first.ID] == ids[second.ID] || ids[parent.ID] == ids[child.ID] {
		t.Errorf("short IDs collide: %v", ids)
	}
	if len(ids[first.ID]) < len("ses_")+8 {
		t.Errorf("opencode short ID = %q, want at least ses_ plus eight characters", ids[first.ID])
	}
}

func TestLsJSONKeepsTheFullIDAndItsKeys(t *testing.T) {
	now := time.Now().UnixMilli()
	s := store.Session{ID: "claude:1043899d-ee11-4a11", Tool: "claude", NativeID: "1043899d-ee11-4a11", Title: "Audit", Status: store.StatusIdle, CreatedAt: now, UpdatedAt: now}
	out := captureStdout(t, func() {
		if err := printJSON(tree.Build([]store.Session{s}), nil, map[string][2]string{s.ID: {"api", "lead"}}); err != nil {
			t.Fatal(err)
		}
	})
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want one", len(rows))
	}
	for _, key := range []string{"id", "tool", "native_id", "status", "live", "depth", "team", "role"} {
		if _, ok := rows[0][key]; !ok {
			t.Errorf("JSON row has no %q: %s", key, out)
		}
	}
	var id string
	if err := json.Unmarshal(rows[0]["id"], &id); err != nil || id != s.ID {
		t.Errorf("JSON id = %q, %v; want the full ID %q", id, err, s.ID)
	}
}
