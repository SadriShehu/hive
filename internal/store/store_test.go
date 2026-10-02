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

func TestTrashRestoreAndPurge(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, s := range []Session{
		{ID: "claude:A", Tool: "claude", NativeID: "A", Cwd: "/src", CreatedAt: 1, UpdatedAt: 1},
		{ID: "opencode:B", Tool: "opencode", NativeID: "B", ParentID: "claude:A", CreatedAt: 2, UpdatedAt: 2},
		{ID: "claude:C", Tool: "claude", NativeID: "C", ParentID: "opencode:B", CreatedAt: 3, UpdatedAt: 3},
		{ID: "claude:D", Tool: "claude", NativeID: "D", ParentID: "claude:A", CreatedAt: 4, UpdatedAt: 4},
		{ID: "claude:E", Tool: "claude", NativeID: "E", CreatedAt: 5, UpdatedAt: 5},
	} {
		must(t, st.Upsert(s))
	}
	family := func(id string, trashed bool) string {
		t.Helper()
		list, err := st.Family(id, trashed)
		must(t, err)
		return ids(list)
	}
	if got := family("claude:A", false); got != "claude:A opencode:B claude:C claude:D" {
		t.Fatalf("family = %s", got)
	}

	// A branch in the trash already is left out of what goes next.
	must(t, st.Trash([]string{"claude:C"}, 5))
	if got := family("claude:A", false); got != "claude:A opencode:B claude:D" {
		t.Fatalf("family without the trashed branch = %s", got)
	}
	must(t, st.Trash([]string{"claude:A", "opencode:B", "claude:D"}, 10))
	all, _ := st.All()
	trashed, _ := st.Trashed()
	if ids(all) != "claude:E" || len(trashed) != 4 {
		t.Fatalf("all = %s, trashed = %s", ids(all), ids(trashed))
	}
	if got := family("opencode:B", true); got != "opencode:B claude:C" {
		t.Fatalf("family in the trash = %s", got)
	}
	if c, _ := st.ClaimCandidates("claude", "/src", 0); len(c) != 0 {
		t.Errorf("a session in the trash can be claimed: %s", ids(c))
	}

	// Imports and children naming it leave a session in the trash; its agent
	// reporting in brings it back.
	must(t, st.Upsert(Session{ID: "claude:A", Tool: "claude", NativeID: "A", Title: "renamed", Source: SourceImport, CreatedAt: 1, UpdatedAt: 1}))
	must(t, st.Upsert(Session{ID: "claude:D", Tool: "claude", NativeID: "D", Source: SourceInferred, CreatedAt: 4, UpdatedAt: 4}))
	if a, _, _ := st.Get("claude:A"); a.DeletedAt != 10 || a.Title != "renamed" {
		t.Errorf("A after an import = %+v, want it still in the trash, renamed", a)
	}
	if d, _, _ := st.Get("claude:D"); d.DeletedAt != 10 {
		t.Errorf("a child naming D brought it back")
	}
	must(t, st.Upsert(Session{ID: "claude:D", Tool: "claude", NativeID: "D", Source: SourceHook, CreatedAt: 4, UpdatedAt: 4}))
	if d, _, _ := st.Get("claude:D"); d.DeletedAt != 0 {
		t.Errorf("D stayed in the trash after its agent reported in")
	}
	must(t, st.Restore([]string{"claude:A"}))
	if a, _, _ := st.Get("claude:A"); a.DeletedAt != 0 {
		t.Errorf("A still in the trash after restoring it")
	}

	// Deleting for good takes the commands the session ran, but keeps what
	// matched it as the session a command started.
	must(t, st.PutHint(Hint{ParentID: "opencode:B", At: 3, Tool: "claude"}))
	must(t, st.CloseHint(Hint{ParentID: "opencode:B", At: 3, Tool: "claude"}, "claude:C"))
	must(t, st.PutHint(Hint{ParentID: "claude:C", At: 4, Tool: "opencode"}))
	must(t, st.PutHint(Hint{ParentID: "claude:E", At: 6, Tool: "claude"}))
	must(t, st.PutLaunch(Launch{Pane: "%1", Tool: "claude", ParentID: "claude:C", CreatedAt: 7}))
	must(t, st.Purge("claude:C", 20))
	if _, ok, _ := st.Get("claude:C"); ok {
		t.Error("C still recorded")
	}
	if hints, _ := st.OpenHints(); len(hints) != 1 || hints[0].ParentID != "claude:E" {
		t.Errorf("open hints = %+v, want only E's", hints)
	}
	if launches, _ := st.Launches(); len(launches) != 0 {
		t.Errorf("launches = %+v, want C's gone", launches)
	}
	must(t, st.Upsert(Session{ID: "claude:C", Tool: "claude", NativeID: "C", Source: SourceImport, CreatedAt: 3, UpdatedAt: 3}))
	if _, ok, _ := st.Get("claude:C"); ok {
		t.Fatal("an import brought back a session deleted for good")
	}
	if purged, _ := st.PurgedIDs(); !purged["claude:C"] {
		t.Errorf("purged = %v", purged)
	}
	must(t, st.Upsert(Session{ID: "claude:C", Tool: "claude", NativeID: "C", Source: SourceHook, CreatedAt: 3, UpdatedAt: 3}))
	if _, ok, _ := st.Get("claude:C"); !ok {
		t.Fatal("C's agent reported in, and C stayed gone")
	}
	if purged, _ := st.PurgedIDs(); purged["claude:C"] {
		t.Error("C is back, and still counts as deleted for good")
	}
}

// A hive from before the trash sets the schema version back when it opens the
// database; the trash must survive that, and a newer version must stay.
func TestMigrationsRunAgainAfterAnOlderHive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hive.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	must(t, st.Upsert(Session{ID: "claude:A", Tool: "claude", NativeID: "A", CreatedAt: 1, UpdatedAt: 1}))
	must(t, st.Trash([]string{"claude:A"}, 5))
	_, err = st.db.Exec(`PRAGMA user_version = 2`)
	must(t, err)
	st.Close()

	if st, err = Open(path); err != nil {
		t.Fatalf("reopening after an older hive: %v", err)
	}
	if a, _, _ := st.Get("claude:A"); a.DeletedAt != 5 {
		t.Errorf("A = %+v, want it still in the trash", a)
	}
	_, err = st.db.Exec(`PRAGMA user_version = 99`)
	must(t, err)
	st.Close()
	if st, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var version int
	must(t, st.db.QueryRow(`PRAGMA user_version`).Scan(&version))
	if version != 99 {
		t.Errorf("version = %d, want a newer hive's 99 kept", version)
	}
}

func ids(list []Session) string {
	var out []string
	for _, s := range list {
		out = append(out, s.ID)
	}
	return strings.Join(out, " ")
}

func TestInputsWaitForTheirAnswer(t *testing.T) {
	st := open(t)
	must(t, st.SetInput("%4", 100)) // typed into a pane before its agent named its session
	must(t, st.MoveInput("%4", "opencode:a"))
	if _, pending, _ := st.Input("%4"); pending {
		t.Fatal("the pane kept the input after handing it over")
	}
	if at, pending, err := st.Input("opencode:a"); err != nil || !pending || at != 100 {
		t.Fatalf("Input = %d, %v, %v; want 100, pending", at, pending, err)
	}

	must(t, st.AnswerInput("opencode:a", 99)) // a turn that ended before the message
	if _, pending, _ := st.Input("opencode:a"); !pending {
		t.Fatal("an older turn answered a newer message")
	}
	must(t, st.AnswerInput("opencode:a", 100))
	if _, pending, _ := st.Input("opencode:a"); pending {
		t.Fatal("a turn ending with the message didn't answer it")
	}

	must(t, st.Upsert(Session{ID: "claude:b", Tool: "claude", NativeID: "b", CreatedAt: 1, UpdatedAt: 1}))
	must(t, st.SetInput("claude:b", 5))
	must(t, st.Purge("claude:b", 10))
	if _, pending, _ := st.Input("claude:b"); pending {
		t.Fatal("deleting a session for good kept its input")
	}
}
