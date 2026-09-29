package tracker

import (
	"strings"
	"testing"
	"time"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tmux"
)

func TestLookup(t *testing.T) {
	sessions := []store.Session{
		{ID: "claude:aaa111", Tool: "claude", NativeID: "aaa111", UpdatedAt: 1},
		{ID: "claude:aaa222", Tool: "claude", NativeID: "aaa222", UpdatedAt: 2},
		{ID: "opencode:ses_x", Tool: "opencode", NativeID: "ses_x"},
	}
	for ref, want := range map[string]string{
		"claude:aaa111": "claude:aaa111", // full ID
		"aaa222":        "claude:aaa222", // the tool's own ID
		"aaa1":          "claude:aaa111", // unique prefix of the tool's ID
		"opencode:":     "opencode:ses_x",
		" ses_x ":       "opencode:ses_x",
	} {
		s, err := Lookup(sessions, ref)
		if err != nil || s.ID != want {
			t.Errorf("Lookup(%q) = %q, %v; want %q", ref, s.ID, err, want)
		}
	}
	for _, ref := range []string{"", "zzz", "claude"} {
		if s, err := Lookup(sessions, ref); err == nil {
			t.Errorf("Lookup(%q) = %q, want an error", ref, s.ID)
		}
	}
	// An ambiguous prefix names the candidates, most recent first.
	if _, err := Lookup(sessions, "aaa"); err == nil || !strings.Contains(err.Error(), "claude:aaa222, claude:aaa111") {
		t.Errorf("ambiguous Lookup error = %v", err)
	}
}

func TestWaitForSession(t *testing.T) {
	tr, w := newWorld(t)
	w.add(4242, 1, "opencode")
	w.panes = append(w.panes, tmux.Pane{ID: "%7", PID: 4242})
	id := "opencode:ses_new"
	if err := tr.Store.Upsert(store.Session{ID: id, Tool: "opencode", NativeID: "ses_new", CreatedAt: 1_000, UpdatedAt: 1_000}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Store.Attach(id, 4242, "%7", store.KindInteractive); err != nil {
		t.Fatal(err)
	}
	if err := tr.Store.SetStatus(id, store.StatusIdle, 1_500); err != nil {
		t.Fatal(err)
	}
	if s, err := tr.WaitForSession("opencode", "%7", 1_200, time.Minute, time.Second); err != nil || s.ID != id {
		t.Fatalf("WaitForSession = %q, %v", s.ID, err)
	}
	// Nothing reported since, or on another pane or tool: time out.
	for _, c := range []struct {
		tool, pane string
		after      int64
	}{{"opencode", "%7", 1_500}, {"opencode", "%8", 0}, {"claude", "%7", 0}} {
		if _, err := tr.WaitForSession(c.tool, c.pane, c.after, time.Minute, 50*time.Millisecond); err == nil {
			t.Errorf("WaitForSession(%s, %s, %d) found a session", c.tool, c.pane, c.after)
		}
	}
}

func TestWaitForSessionSettlesForPending(t *testing.T) {
	tr, w := newWorld(t)
	// opencode without a prompt: running in the window hive opened, silent.
	w.add(4300, 1, "opencode")
	w.panes = append(w.panes, tmux.Pane{ID: "%9", PID: 4300})
	if err := tr.Store.PutLaunch(store.Launch{Pane: "%9", Tool: "opencode", ParentID: "claude:c1", CreatedAt: w.now}); err != nil {
		t.Fatal(err)
	}
	s, err := tr.WaitForSession("opencode", "%9", 0, 0, time.Second)
	if err != nil || s.ID != "opencode:pid-4300" || s.ParentID != "claude:c1" {
		t.Fatalf("WaitForSession = %+v, %v", s, err)
	}
	// Its first message starts the session; the stand-in ID now finds it.
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "ses_late", Type: agent.Prompt, PID: 4300})
	sessions, err := tr.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"opencode:pid-4300", "pid-4300"} {
		if got, err := Lookup(sessions, ref); err != nil || got.ID != "opencode:ses_late" {
			t.Errorf("Lookup(%s) = %q, %v", ref, got.ID, err)
		}
	}
	if _, err := Lookup(sessions, "claude:pid-4300"); err == nil {
		t.Error("a stand-in ID matched another tool's session")
	}
}

func TestCaller(t *testing.T) {
	tr, w := newWorld(t)
	if got := tr.Caller(); got != "" {
		t.Fatalf("Caller outside any agent = %q", got)
	}
	// A Claude session runs `hive new` through its shell tool.
	claudeHook(t, tr, w, 100, "c1", agent.Start)
	w.add(700, 100, "/bin/zsh -c hive new opencode")
	w.add(701, 700, "hive new opencode")
	w.self = 701
	if got := tr.Caller(); got != "claude:c1" {
		t.Errorf("Caller under claude = %q", got)
	}
	// Outside any session's process tree, the environment names the caller.
	w.add(800, 1, "hive new claude")
	w.self = 800
	w.env[ParentEnv] = "opencode:ses_p"
	if got := tr.Caller(); got != "opencode:ses_p" {
		t.Errorf("Caller from %s = %q", ParentEnv, got)
	}
}
