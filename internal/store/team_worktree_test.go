package store

import "testing"

func TestWorktreesAndBaseRoundTrip(t *testing.T) {
	st := teamStore(t)
	if _, ok, err := st.TeamBase("t1"); ok || err != nil {
		t.Fatalf("a shared team has a base: %v %v", ok, err)
	}
	must(t, st.PutTeamBase(TeamBase{TeamID: "t1", Branch: "main", Head: "abc", CreatedAt: 1}))
	if b, ok, _ := st.TeamBase("t1"); !ok || b.Branch != "main" || b.Head != "abc" {
		t.Errorf("base = %+v, %v", b, ok)
	}
	for _, role := range []string{"backend", "docs"} {
		must(t, st.PutWorktree(Worktree{TeamID: "t1", Role: role, Path: "/wt/" + role, Branch: "hive/api/" + role, CreatedAt: 2}))
	}
	must(t, st.ForgetWorktree("t1", "backend"))
	if wts, _ := st.Worktrees("t1"); len(wts) != 1 || wts[0].Role != "docs" || wts[0].Branch != "hive/api/docs" {
		t.Errorf("worktrees = %+v, want docs's only", wts)
	}
	must(t, st.SetMemberCwd("t1", "backend", "/wt/backend"))
	if ms, _ := st.Members("t1"); ms[1].Cwd != "/wt/backend" {
		t.Errorf("backend works in %q", ms[1].Cwd)
	}
	must(t, st.ForgetWorktrees("t1"))
	if wts, _ := st.Worktrees("t1"); len(wts) != 0 {
		t.Errorf("worktrees left: %+v", wts)
	}
	if _, ok, _ := st.TeamBase("t1"); ok {
		t.Error("the base is left")
	}
}

func TestDropQueuedLeavesWhatWasDelivered(t *testing.T) {
	st := teamStore(t)
	st.AddMessage(Message{TeamID: "t1", From: "lead", To: "backend", Body: "a", Kind: MsgSay, At: 5}, []string{"backend"})
	st.ClaimPending("t1", "backend", 6)
	st.AddMessage(Message{TeamID: "t1", From: "lead", To: "all", Body: "b", Kind: MsgSay, At: 7}, []string{"backend", "lead"})
	n, err := st.DropQueued("t1")
	if err != nil || n != 2 {
		t.Fatalf("DropQueued = %d, %v; want the 2 waiting", n, err)
	}
	if pending, _ := st.HasPending("t1", "backend"); pending {
		t.Error("mail still waits")
	}
	if msgs, _ := st.Messages("t1", 10, true); len(msgs) != 2 {
		t.Errorf("channel = %+v, want both messages kept", msgs)
	}
}
