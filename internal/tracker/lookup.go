package tracker

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sadrishehu/hive/internal/store"
)

// ErrNoSession is what Lookup returns when nothing matches.
var ErrNoSession = errors.New("no such session")

// Lookup finds the session ref names: its full ID ("claude:abc…"), the tool's
// own ID, or a prefix of either that only one session has.
func Lookup(sessions []store.Session, ref string) (store.Session, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return store.Session{}, errors.New("no session given")
	}
	var matches []store.Session
	for _, s := range sessions {
		if s.ID == ref || s.NativeID == ref {
			return s, nil
		}
		if strings.HasPrefix(s.ID, ref) || strings.HasPrefix(s.NativeID, ref) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return store.Session{}, fmt.Errorf("%w: nothing matches %q; `hive ls --all` lists them", ErrNoSession, ref)
	case 1:
		return matches[0], nil
	}
	slices.SortFunc(matches, func(a, b store.Session) int { return cmp.Compare(b.UpdatedAt, a.UpdatedAt) })
	var ids []string
	for _, s := range matches[:min(len(matches), 4)] {
		ids = append(ids, s.ID)
	}
	if len(matches) > 4 {
		ids = append(ids, "…")
	}
	return store.Session{}, fmt.Errorf("%q matches %d sessions: %s", ref, len(matches), strings.Join(ids, ", "))
}

// WaitForSession waits until the agent started in pane reports in: an event
// of its session lands after the time after. Tools that choose their own ID
// are known only from then on.
func (t *Tracker) WaitForSession(tool, pane string, after int64, timeout time.Duration) (store.Session, error) {
	deadline := time.Now().Add(timeout)
	for {
		sessions, err := t.Store.All()
		if err != nil {
			return store.Session{}, err
		}
		for _, s := range sessions {
			if s.Tool == tool && s.Pane == pane && !s.Synthetic() && s.StatusAt > after {
				return s, nil
			}
		}
		if time.Now().After(deadline) {
			return store.Session{}, fmt.Errorf("%s hasn't reported in after %s; it may start its session "+
				"only with the first message, or its hooks aren't installed (`hive doctor`)", tool, timeout)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
