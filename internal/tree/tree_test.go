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
