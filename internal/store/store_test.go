package store

import (
	"path/filepath"
	"sync"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestUpsertMerges(t *testing.T) {
	st := open(t)
	must(t, st.Upsert(Session{ID: "claude:a", Tool: "claude", NativeID: "a", ParentID: "opencode:p",
		Cwd: "/one", Kind: KindHeadless, Title: "first", CreatedAt: 200, UpdatedAt: 200}))
	must(t, st.Upsert(Session{ID: "claude:a", Tool: "claude", NativeID: "a", ParentID: "claude:other",
		Cwd: "/two", Kind: KindInteractive, Title: "", CreatedAt: 100, UpdatedAt: 300}))

	s, ok, err := st.Get("claude:a")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	want := Session{ID: "claude:a", Tool: "claude", NativeID: "a", ParentID: "opencode:p", Cwd: "/one",
		Kind: KindHeadless, Title: "first", Status: StatusUnknown, CreatedAt: 100, UpdatedAt: 300}
	if s != want {
		t.Fatalf("got  %+v\nwant %+v", s, want)
	}
}

func TestSetStatusIgnoresOlderEvents(t *testing.T) {
	st := open(t)
	must(t, st.Upsert(Session{ID: "x:1", Tool: "x", NativeID: "1", CreatedAt: 1, UpdatedAt: 1}))
	must(t, st.Attach("x:1", 42, "%3", KindInteractive))
	must(t, st.SetStatus("x:1", StatusWorking, 20))
	must(t, st.SetStatus("x:1", StatusIdle, 10))
	s, _, _ := st.Get("x:1")
	if s.Status != StatusWorking || s.PID != 42 || s.Pane != "%3" {
		t.Fatalf("got %+v, want working in pid 42 / %%3", s)
	}
	must(t, st.SetStatus("x:1", StatusExited, 30))
	s, _, _ = st.Get("x:1")
	if s.Status != StatusExited || s.PID != 0 || s.Pane != "" || s.Live() {
		t.Fatalf("got %+v, want exited with no process", s)
	}
}

func TestLaunchesExpire(t *testing.T) {
	st := open(t)
	must(t, st.PutLaunch(Launch{Pane: "%1", Tool: "claude", ParentID: "claude:p", CreatedAt: 100}))
	if _, fresh, _ := st.TakeLaunch("%1", 200); fresh {
		t.Fatal("stale launch returned as fresh")
	}
	must(t, st.PutLaunch(Launch{Pane: "%1", Tool: "claude", ParentID: "claude:p", CreatedAt: 300}))
	if l, fresh, _ := st.TakeLaunch("%1", 200); !fresh || l.ParentID != "claude:p" {
		t.Fatalf("launch = %+v fresh=%v", l, fresh)
	}
	if _, fresh, _ := st.TakeLaunch("%1", 0); fresh {
		t.Fatal("launch returned twice")
	}
}

// Hooks from many agents open the same fresh database at once.
func TestConcurrentOpenAndWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hive.db")
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		wg.Go(func() {
			st, err := Open(path)
			if err != nil {
				errs <- err
				return
			}
			defer st.Close()
			id := ID("t", string(rune('a'+i)))
			errs <- st.Upsert(Session{ID: id, Tool: "t", NativeID: id, CreatedAt: 1, UpdatedAt: 1})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		must(t, err)
	}
	st, err := Open(path)
	must(t, err)
	defer st.Close()
	all, err := st.All()
	must(t, err)
	if len(all) != 20 {
		t.Fatalf("%d sessions, want 20", len(all))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
