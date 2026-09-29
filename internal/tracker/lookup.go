package tracker

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
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
	for _, s := range sessions {
		if s.ID == ref || s.NativeID == ref {
			return s, nil
		}
	}
	if s, ok := byPID(sessions, ref); ok {
		return s, nil
	}
	var matches []store.Session
	for _, s := range sessions {
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

var pidRef = regexp.MustCompile(`^(?:([a-z0-9_-]+):)?pid-(\d+)$`)

// byPID finds the session now running in process N for a stand-in ID
// ("opencode:pid-N"), which a pending session goes by until it reports in.
func byPID(sessions []store.Session, ref string) (store.Session, bool) {
	m := pidRef.FindStringSubmatch(ref)
	if m == nil {
		return store.Session{}, false
	}
	pid, _ := strconv.Atoi(m[2])
	for _, s := range sessions {
		if s.PID == pid && (m[1] == "" || s.Tool == m[1]) && s.Live() && !s.Synthetic() {
			return s, true
		}
	}
	return store.Session{}, false
}

// WaitForSession waits until the agent started in pane reports in: an event
// of its session lands after the time after. A tool that starts its session
// only with its first message never does, so once its process has been up
// for quiet without a word, its pending session is returned instead: its
// stand-in ID keeps finding the session after it starts (see Lookup).
func (t *Tracker) WaitForSession(tool, pane string, after int64, quiet, timeout time.Duration) (store.Session, error) {
	deadline := time.Now().Add(timeout)
	var up time.Time // when the agent was first seen running, still silent
	for {
		t.World.Forget() // the agent's process and pane appear after the first look
		sessions, err := t.Refresh()
		if err != nil {
			return store.Session{}, err
		}
		for _, s := range sessions {
			if s.Tool != tool || s.Pane != pane {
				continue
			}
			switch {
			case !s.Synthetic() && s.StatusAt > after:
				return s, nil
			case s.Source == "pending" && up.IsZero():
				up = time.Now()
			case s.Source == "pending" && time.Since(up) >= quiet:
				return s, nil
			}
		}
		if time.Now().After(deadline) {
			return store.Session{}, fmt.Errorf("%s hasn't reported in after %s; are its hooks installed? (`hive doctor`)", tool, timeout)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
