package store

import (
	"path/filepath"
	"strings"
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

func TestDeleteFamilyAndTombstones(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, s := range []Session{
		{ID: "claude:A", Tool: "claude", NativeID: "A", CreatedAt: 1, UpdatedAt: 1},
		{ID: "opencode:B", Tool: "opencode", NativeID: "B", ParentID: "claude:A", CreatedAt: 2, UpdatedAt: 2},
		{ID: "claude:C", Tool: "claude", NativeID: "C", ParentID: "opencode:B", CreatedAt: 3, UpdatedAt: 3},
		{ID: "claude:D", Tool: "claude", NativeID: "D", ParentID: "claude:A", CreatedAt: 4, UpdatedAt: 4},
		{ID: "claude:E", Tool: "claude", NativeID: "E", CreatedAt: 5, UpdatedAt: 5},
	} {
		if err := st.Upsert(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PutHint(Hint{ParentID: "opencode:B", At: 3, Tool: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutHint(Hint{ParentID: "claude:E", At: 6, Tool: "claude"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutLaunch(Launch{Pane: "%1", Tool: "claude", ParentID: "claude:A", CreatedAt: 7}); err != nil {
		t.Fatal(err)
	}
	family, err := st.Family("claude:A")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range family {
		ids = append(ids, s.ID)
	}
	if strings.Join(ids, " ") != "claude:A opencode:B claude:C claude:D" {
		t.Fatalf("family = %v", ids)
	}
	if err := st.Delete(ids, 10); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, ok, _ := st.Get(id); ok {
			t.Errorf("%s still recorded", id)
		}
	}
	if _, ok, _ := st.Get("claude:E"); !ok {
		t.Error("an unrelated session was deleted")
	}
	hints, _ := st.OpenHints()
	if len(hints) != 1 || hints[0].ParentID != "claude:E" {
		t.Errorf("hints = %+v, want only E's", hints)
	}
	if launches, _ := st.Launches(); len(launches) != 0 {
		t.Errorf("launches = %+v, want the deleted parent's gone", launches)
	}
	deleted, _ := st.DeletedIDs()
	if len(deleted) != 4 || !deleted["claude:C"] {
		t.Errorf("deleted = %v", deleted)
	}
	if err := st.Upsert(Session{ID: "claude:C", Tool: "claude", NativeID: "C", Source: SourceImport, CreatedAt: 3, UpdatedAt: 3}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.Get("claude:C"); ok {
		t.Error("an import brought a deleted session back")
	}
	if err := st.Upsert(Session{ID: "claude:C", Tool: "claude", NativeID: "C", Source: "hook", CreatedAt: 3, UpdatedAt: 3}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.Get("claude:C"); !ok {
		t.Fatal("a hook event did not bring the session back")
	}
	if deleted, _ := st.DeletedIDs(); deleted["claude:C"] {
		t.Error("the tombstone stayed after the session came back")
	}
	if err := st.Upsert(Session{ID: "claude:C", Tool: "claude", NativeID: "C", Title: "again", Source: SourceImport, CreatedAt: 3, UpdatedAt: 3}); err != nil {
		t.Fatal(err)
	}
	if s, _, _ := st.Get("claude:C"); s.Title != "again" {
		t.Error("imports still skip the session after it came back")
	}
}
