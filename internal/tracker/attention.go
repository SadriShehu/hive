package tracker

import (
	"cmp"
	"slices"
	"strings"

	"github.com/sadrishehu/hive/internal/store"
)

// PaneFor returns the pane that shows s: its own, or for a tool's own
// subagent, living inside its parent's process, the nearest ancestor's. get
// looks a session up by ID.
func PaneFor(s store.Session, get func(id string) (store.Session, bool)) string {
	for range 32 {
		if s.Pane != "" {
			return s.Pane
		}
		if s.Kind != store.KindInternal {
			return ""
		}
		parent, ok := get(s.ParentID)
		if !ok {
			return ""
		}
		s = parent
	}
	return ""
}

// NeedsYou returns the live sessions waiting on the user, longest waiting
// first.
func NeedsYou(sessions []store.Session) []store.Session {
	var out []store.Session
	for _, s := range sessions {
		if s.Live() && s.Status == store.StatusAttention {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b store.Session) int {
		return cmp.Or(cmp.Compare(a.StatusAt, b.StatusAt), strings.Compare(a.ID, b.ID))
	})
	return out
}

// Done reports whether s is through with what it was given, and where that
// left it: exited; or, once its agent has answered every message hive gave
// it, waiting on the user (attention) or idle. A session whose agent doesn't
// report its status is done only when it ends.
func (t *Tracker) Done(s store.Session) (string, bool, error) {
	if !s.Live() {
		return store.StatusExited, true, nil
	}
	if _, pending, err := t.Store.Input(inputKey(s)); err != nil || pending {
		return "", false, err
	}
	switch s.Status {
	case store.StatusAttention, store.StatusIdle:
		return s.Status, true, nil
	}
	return "", false, nil
}

// inputKey is where a message hive gives s is recorded: under its ID, or
// under its pane while hive doesn't know its ID yet.
func inputKey(s store.Session) string {
	if s.Synthetic() {
		return s.Pane
	}
	return s.ID
}
