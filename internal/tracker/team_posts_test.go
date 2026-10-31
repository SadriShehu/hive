package tracker

import (
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
)

func TestAMemberThatDiesWithoutAnEndEventIsPostedStoppedOnce(t *testing.T) {
	tr, w, _ := newTeamWorld(t)
	backendUp(t, tr, w)

	// Its window is closed (hive kill, x, a crash): no end hook runs. Every
	// process that looks finds it gone; one says so.
	delete(w.procs, 100)
	w.now = 2_000
	for range 2 {
		if _, err := tr.Refresh(); err != nil {
			t.Fatal(err)
		}
	}
	if got := hivePosts(tr); strings.Count(got, "backend stopped") != 1 {
		t.Fatalf("hive posts = %q, want backend stopped once", got)
	}
	// An end event that comes late says nothing more.
	claudeHook(t, tr, w, 100, "A", agent.End)
	if got := hivePosts(tr); strings.Count(got, "backend stopped") != 1 {
		t.Fatalf("hive posts after a late end = %q, want backend stopped once", got)
	}
}
