package tracker

import (
	"context"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

func TestATeamGoesToTheTrashWithItsSessions(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)

	if _, err := tr.TrashTeam(tm); err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("trashing a team whose backend runs: %v", err)
	}
	delete(w.procs, 100)
	w.now = 2_000
	if _, err := tr.Refresh(); err != nil {
		t.Fatal(err)
	}

	family, err := tr.TrashTeam(tm)
	if err != nil || len(family) != 1 || family[0].ID != "claude:A" {
		t.Fatalf("trash: family=%+v err=%v", family, err)
	}
	if _, err := tr.Store.Team("api"); err == nil {
		t.Fatal("a trashed team is still in the teams")
	}
	teams, _ := tr.Store.TrashedTeams()
	if len(teams) != 1 || teams[0].ID != "t1" {
		t.Fatalf("TrashedTeams = %+v", teams)
	}
	if get(t, tr, "claude:A").DeletedAt == 0 {
		t.Fatal("the member's session stayed out of the trash")
	}

	restored, err := tr.RestoreTeam(tm)
	if err != nil || len(restored) != 1 {
		t.Fatalf("restore: family=%+v err=%v", restored, err)
	}
	if _, err := tr.Store.Team("api"); err != nil {
		t.Fatalf("the team didn't come back: %v", err)
	}
	if get(t, tr, "claude:A").DeletedAt != 0 {
		t.Fatal("the member's session stayed in the trash")
	}
}

func TestPurgeTeamDeletesItsMembersThroughTheirTools(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	var purged []string
	for i, a := range tr.Adapters {
		tr.Adapters[i] = &fakePurger{Adapter: a, purged: &purged}
	}
	backendUp(t, tr, w)
	tm := theTeam(t, tr)
	delete(w.procs, 100)
	w.now = 2_000
	if _, err := tr.TrashTeam(tm); err != nil {
		t.Fatal(err)
	}

	out, err := tr.PurgeTeam(context.Background(), tm)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(purged, " ") != "claude:A" || len(out.Sessions) != 1 {
		t.Fatalf("purged=%v out=%+v", purged, out)
	}
	if _, ok, _ := tr.Store.Get("claude:A"); ok {
		t.Fatal("the member's session is still recorded")
	}
	if teams, _ := tr.Store.TrashedTeams(); len(teams) != 0 {
		t.Fatalf("TrashedTeams = %+v, want the team gone", teams)
	}
}

func TestASessionOnTwoTeamsStaysForTheOther(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)
	if err := tr.Store.CreateTeam(store.Team{ID: "t2", Name: "other", Cwd: "/work", Workspace: store.WorkspaceShared,
		Status: store.TeamActive, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Store.AddMember(store.Member{TeamID: "t2", Role: "backend", SessionID: "claude:A", Tool: "claude", JoinedAt: 1}); err != nil {
		t.Fatal(err)
	}
	delete(w.procs, 100)
	w.now = 2_000
	if _, err := tr.Refresh(); err != nil {
		t.Fatal(err)
	}

	if _, err := tr.TrashTeam(tm); err != nil {
		t.Fatal(err)
	}
	if get(t, tr, "claude:A").DeletedAt != 0 {
		t.Fatal("a session on another team went to the trash with this one")
	}
}

// Retiring a member takes its session off the team: restoring the team later
// leaves a session the user trashed on its own where the user put it.
func TestRestoreLeavesARetiredMembersSessionAlone(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)
	claudeHook(t, tr, w, 100, "A", agent.End)
	if err := tr.RetireMember(tm, "backend"); err != nil {
		t.Fatal(err)
	}
	s := get(t, tr, "claude:A")
	if _, err := tr.Trash(s); err != nil {
		t.Fatal(err)
	}

	if _, err := tr.RestoreTeam(tm); err != nil {
		t.Fatal(err)
	}
	if get(t, tr, "claude:A").DeletedAt == 0 {
		t.Fatal("restoring the team took back a retired member's session the user trashed")
	}
}

// Purging a team leaves a retired member's session too: the team no longer
// plays it, so it is the user's to purge on its own.
func TestPurgeLeavesARetiredMembersSessionAlone(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)
	claudeHook(t, tr, w, 100, "A", agent.End)
	if err := tr.RetireMember(tm, "backend"); err != nil {
		t.Fatal(err)
	}
	s := get(t, tr, "claude:A")
	if _, err := tr.Trash(s); err != nil {
		t.Fatal(err)
	}
	if err := tr.Store.TrashTeam(tm.ID, w.now); err != nil {
		t.Fatal(err)
	}

	if _, err := tr.PurgeTeam(context.Background(), tm); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := tr.Store.Get("claude:A"); err != nil || !ok {
		t.Fatalf("purging the team deleted a retired member's session: ok=%v err=%v", ok, err)
	}
}

// A session on two teams goes to the trash with the second, and a purge of
// the first leaves it so the second can still restore it.
func TestPurgeLeavesASessionAnotherTrashedTeamPlays(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)
	if err := tr.Store.CreateTeam(store.Team{ID: "t2", Name: "other", Cwd: "/work", Workspace: store.WorkspaceShared,
		Status: store.TeamActive, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Store.AddMember(store.Member{TeamID: "t2", Role: "backend", SessionID: "claude:A", Tool: "claude", JoinedAt: 1}); err != nil {
		t.Fatal(err)
	}
	other, err := tr.Store.Team("other")
	if err != nil {
		t.Fatal(err)
	}
	delete(w.procs, 100)
	w.now = 2_000
	if _, err := tr.Refresh(); err != nil {
		t.Fatal(err)
	}

	if _, err := tr.TrashTeam(tm); err != nil {
		t.Fatal(err)
	}
	if get(t, tr, "claude:A").DeletedAt != 0 {
		t.Fatal("a session on another active team went to the trash with this one")
	}
	if _, err := tr.TrashTeam(other); err != nil {
		t.Fatal(err)
	}
	if get(t, tr, "claude:A").DeletedAt == 0 {
		t.Fatal("the session didn't go to the trash with the second team")
	}

	if _, err := tr.PurgeTeam(context.Background(), tm); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := tr.Store.Get("claude:A"); err != nil || !ok {
		t.Fatalf("purging one team deleted a session the other trashed team plays: ok=%v err=%v", ok, err)
	}
	if _, err := tr.RestoreTeam(other); err != nil {
		t.Fatal(err)
	}
	if get(t, tr, "claude:A").DeletedAt != 0 {
		t.Fatal("the other team couldn't restore the session the purge left")
	}
}
