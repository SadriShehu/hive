package store

import (
	"strings"
	"testing"
)

func teamStore(t *testing.T) *Store {
	t.Helper()
	st := open(t)
	if err := st.CreateTeam(Team{ID: "t1", Name: "api", Cwd: "/work", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	for i, role := range []string{"lead", "backend"} {
		if err := st.AddMember(Member{TeamID: "t1", Role: role, Tool: "claude", JoinedAt: int64(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestTeamsAndMembersRoundTrip(t *testing.T) {
	st := teamStore(t)

	if err := st.CreateTeam(Team{ID: "t2", Name: "api", Cwd: "/other", CreatedAt: 2, UpdatedAt: 2}); err == nil {
		t.Error("two teams with one name")
	}
	if err := st.AddMember(Member{TeamID: "t1", Role: "lead", Tool: "codex", JoinedAt: 3}); err == nil {
		t.Error("a role twice on one team")
	}
	if tm, err := st.Team("ap"); err != nil || tm.ID != "t1" {
		t.Errorf("Team(prefix) = %+v, %v", tm, err)
	}
	if _, err := st.Team("zzz"); err == nil {
		t.Error("Team found a team that isn't there")
	}

	if err := st.SetMemberSession("t1", "backend", "claude:A", "%3"); err != nil {
		t.Fatal(err)
	}
	ms, err := st.Memberships("claude:A")
	if err != nil || len(ms) != 1 || ms[0].Role != "backend" || ms[0].Pane != "%3" {
		t.Fatalf("Memberships = %+v %v", ms, err)
	}

	// A retired member is off the team, can be taken again, and keeps no queued mail.
	st.AddMessage(Message{TeamID: "t1", From: "lead", To: "backend", Body: "x", Kind: MsgSay, At: 5}, []string{"backend"})
	if err := st.RetireMember("t1", "backend", 6); err != nil {
		t.Fatal(err)
	}
	if ms, _ := st.Memberships("claude:A"); len(ms) != 0 {
		t.Error("a retired member still plays its role")
	}
	if pending, _ := st.HasPending("t1", "backend"); pending {
		t.Error("a retired member keeps its queued mail")
	}
	if err := st.AddMember(Member{TeamID: "t1", Role: "backend", Tool: "codex", JoinedAt: 7}); err != nil {
		t.Errorf("a retired role can't be taken again: %v", err)
	}
}

func TestMessagesQueueAndAreClaimedOnce(t *testing.T) {
	st := teamStore(t)
	for i, body := range []string{"one", "two", "three"} {
		if _, err := st.AddMessage(Message{TeamID: "t1", From: "lead", To: "backend", Body: body, Kind: MsgSay, At: int64(10 + i)}, []string{"backend"}); err != nil {
			t.Fatal(err)
		}
	}
	st.AddMessage(Message{TeamID: "t1", From: "backend", Body: "progress", Kind: MsgPost, At: 20}, nil)

	got, err := st.ClaimPending("t1", "backend", 30)
	if err != nil || len(got) != 3 || got[0].Body != "one" || got[2].Body != "three" {
		t.Fatalf("ClaimPending = %+v, %v; want the three in order", got, err)
	}
	if again, _ := st.ClaimPending("t1", "backend", 31); len(again) != 0 {
		t.Fatalf("a second claim took %+v", again)
	}
	if err := st.Unclaim("backend", got[1:]); err != nil {
		t.Fatal(err)
	}
	if left, _ := st.Pending("t1", "backend"); len(left) != 2 || left[0].Body != "two" {
		t.Fatalf("after Unclaim, pending = %+v", left)
	}

	last, _ := st.Messages("t1", 2, true)
	if len(last) != 2 || last[0].Body != "three" || last[1].Body != "progress" {
		t.Fatalf("Messages(2) = %+v, want the last two oldest first", last)
	}
	st.AddMessage(Message{TeamID: "t1", From: "lead", To: "backend", Body: "aside", Kind: MsgSay, Private: true, At: 40}, nil)
	for _, m := range mustMessages(t, st, false) {
		if strings.Contains(m.Body, "aside") {
			t.Error("the feed shows a private message")
		}
	}
}

func mustMessages(t *testing.T, st *Store, all bool) []Message {
	t.Helper()
	msgs, err := st.Messages("t1", 50, all)
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestUnreadCountsOthersPostsPastWhatTheUserSaw(t *testing.T) {
	st := teamStore(t)
	a, _ := st.AddMessage(Message{TeamID: "t1", From: "lead", Body: "plan", Kind: MsgPost, At: 1}, nil)
	st.AddMessage(Message{TeamID: "t1", From: FromYou, To: "lead", Body: "go", Kind: MsgSay, At: 2}, []string{"lead"})
	b, _ := st.AddMessage(Message{TeamID: "t1", From: FromHive, Body: "backend started", Kind: MsgSystem, At: 3}, nil)

	if n, _ := st.Unread(); n["t1"] != 2 {
		t.Fatalf("unread = %v, want 2 (the user's own message doesn't count)", n)
	}
	st.MarkRead("t1", a.ID)
	if n, _ := st.Unread(); n["t1"] != 1 {
		t.Fatalf("unread after reading the first = %v, want 1", n)
	}
	st.MarkRead("t1", b.ID)
	st.MarkRead("t1", a.ID) // never goes back
	if n, _ := st.Unread(); n["t1"] != 0 {
		t.Fatalf("unread after reading all = %v, want 0", n)
	}
}

func TestMembersJoinedTogetherKeepTheirOrder(t *testing.T) {
	st := open(t)
	st.CreateTeam(Team{ID: "t1", Name: "api", Cwd: "/work", CreatedAt: 1, UpdatedAt: 1})
	for _, role := range []string{"lead", "backend", "api"} {
		if err := st.AddMember(Member{TeamID: "t1", Role: role, Tool: "claude", JoinedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	ms, _ := st.Members("t1")
	var roles []string
	for _, m := range ms {
		roles = append(roles, m.Role)
	}
	if got := strings.Join(roles, ","); got != "lead,backend,api" {
		t.Errorf("members = %s, want the order they were added in", got)
	}
}

func TestASessionCanPlayARoleOnSeveralTeams(t *testing.T) {
	st := teamStore(t)
	st.CreateTeam(Team{ID: "t2", Name: "web", Cwd: "/work", CreatedAt: 5, UpdatedAt: 5})
	st.SetMemberSession("t1", "backend", "claude:X", "%1")
	st.AddMember(Member{TeamID: "t2", Role: "lead", Tool: "claude", SessionID: "claude:X", JoinedAt: 5})

	ms, err := st.Memberships("claude:X")
	if err != nil || len(ms) != 2 || ms[0].TeamID != "t2" || ms[1].TeamID != "t1" {
		t.Fatalf("Memberships = %+v, %v; want web's lead, then api's backend", ms, err)
	}
}

func TestLaunchRecordsExpire(t *testing.T) {
	st := teamStore(t)
	for _, l := range []LaunchTeam{
		{Pane: "%1", TeamID: "t1", Role: "backend", CreatedAt: 100},
		{Pane: "%1", TeamID: "t2", Role: "lead", PID: 42, CreatedAt: 100},
		{Pane: "%2", TeamID: "t1", Role: "lead", CreatedAt: 10}, // never reported in
	} {
		if err := st.PutLaunchTeam(l); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.TakeLaunchTeams("%1", 50)
	if err != nil || len(got) != 2 || got[0].PID+got[1].PID != 42 {
		t.Fatalf("TakeLaunchTeams(%%1) = %+v, %v; want both records", got, err)
	}
	if again, _ := st.TakeLaunchTeams("%1", 50); len(again) != 0 {
		t.Errorf("taken twice: %+v", again)
	}
	// The stale record went with the first take, though it was for another pane.
	if stale, _ := st.TakeLaunchTeams("%2", 0); len(stale) != 0 {
		t.Errorf("a record older than since was kept: %+v", stale)
	}
	st.PutLaunchTeam(LaunchTeam{Pane: "%3", TeamID: "t1", Role: "lead", CreatedAt: 10})
	if old, _ := st.TakeLaunchTeams("%3", 50); len(old) != 0 {
		t.Errorf("an expired record for the pane was returned: %+v", old)
	}
}

func TestDeleteTeamTakesItsRows(t *testing.T) {
	st := teamStore(t)
	st.AddMessage(Message{TeamID: "t1", From: "lead", To: "backend", Body: "x", Kind: MsgSay, At: 5}, []string{"backend"})
	st.PutLaunchTeam(LaunchTeam{Pane: "%1", TeamID: "t1", Role: "backend", CreatedAt: 5})
	if err := st.DeleteTeam("t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TeamByID("t1"); err == nil {
		t.Error("the team is still there")
	}
	if ms, _ := st.Members("t1"); len(ms) != 0 {
		t.Errorf("members left: %+v", ms)
	}
	if msgs, _ := st.Messages("t1", 10, true); len(msgs) != 0 {
		t.Errorf("messages left: %+v", msgs)
	}
	if ls, _ := st.TakeLaunchTeams("%1", 0); len(ls) != 0 {
		t.Errorf("launch records left: %+v", ls)
	}
	if err := st.CreateTeam(Team{ID: "t9", Name: "api", Cwd: "/work", CreatedAt: 9, UpdatedAt: 9}); err != nil {
		t.Errorf("the name is still taken: %v", err)
	}
}
