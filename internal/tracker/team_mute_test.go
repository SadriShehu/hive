package tracker

import (
	"strings"
	"testing"
)

func TestMutedMemberQueuesUntilUnmuted(t *testing.T) {
	tr, w, sent := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)

	if err := tr.MuteMember(tm, "backend", true); err != nil {
		t.Fatal(err)
	}
	_, got, err := tr.Say(tm, "lead", "backend", "hold on", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 0 || !strings.Contains(got[0].How, "muted") {
		t.Fatalf("typed %+v, result %+v; want the mail queued", *sent, got)
	}
	if pending, _ := tr.Store.HasPending("t1", "backend"); !pending {
		t.Fatal("the muted member's mail was dropped")
	}
	// A delivery finds it muted too, as one started by its idle hook would.
	if how, _ := tr.Deliver("claude:A", 0); len(*sent) != 0 || !strings.Contains(how, "muted") {
		t.Fatalf("Deliver = %q, typed %+v; want it queued", how, *sent)
	}

	if err := tr.MuteMember(tm, "backend", false); err != nil {
		t.Fatal(err)
	}
	if how, err := tr.Deliver("claude:A", 0); err != nil || how != "sent" {
		t.Fatalf("Deliver = %q, %v", how, err)
	}
	if len(*sent) != 1 || !strings.HasSuffix((*sent)[0].text, "hold on") {
		t.Fatalf("typed %+v after unmuting, want the queued message", *sent)
	}
}

func TestMuteOnlySilencesOneMember(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	backendUp(t, tr, w)
	tm := theTeam(t, tr)

	if err := tr.MuteMember(tm, "backend", true); err != nil {
		t.Fatal(err)
	}
	mutes, err := tr.Store.Mutes(tm.ID)
	if err != nil || len(mutes) != 1 || mutes[0] != "backend" {
		t.Fatalf("Mutes = %v, %v", mutes, err)
	}
	if err := tr.MuteMember(tm, "nobody", true); err == nil || !strings.Contains(err.Error(), "no nobody") {
		t.Fatalf("muting a role off the team: %v, want an error", err)
	}
}
