package store_test

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
)

// Two edits that each read the board before either writes must not leave a
// ring: the store re-reads the board inside the write and the check sees the
// other edit.
func TestAnEditCannotRingTheBoardItDidNotSee(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateTeam(store.Team{ID: "t1", Name: "api", Cwd: "/work", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	first, err := st.AddTask(store.Task{TeamID: "t1", Title: "a", CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.AddTask(store.Task{TeamID: "t1", Title: "b", CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}

	// The edit of #1 waits for #2, decided on a board read before the other
	// edit landed.
	stale := first
	// Another member first makes #2 wait for #1.
	waiting := second
	waiting.After = []int{first.N}
	if ok, err := st.EditTask(second, waiting); err != nil || !ok {
		t.Fatalf("#2 waiting for #1 = %v, %v", ok, err)
	}

	ring := first
	ring.After, ring.UpdatedAt = []int{second.N}, 2
	ok, err := st.UpdateTaskChecked(stale, ring, func(tasks []store.Task) error {
		if team.Cycles(first.N, ring.After, tasks) {
			return errors.New("#1 can't wait for a task that waits for it")
		}
		return nil
	})
	if err == nil || ok {
		t.Fatalf("the edit = %v, %v; want it refused with the ring seen", ok, err)
	}
	if got, err := st.Task("t1", first.N); err != nil || len(got.After) != 0 {
		t.Fatalf("#1 = %+v, %v; want it unchanged", got, err)
	}
}

// A claim made on a board read before another edit added a wait is refused:
// the store re-checks the board it reads inside the write.
func TestAClaimCannotRaceAnEditThatAddsAWait(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateTeam(store.Team{ID: "t1", Name: "api", Cwd: "/work", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	first, err := st.AddTask(store.Task{TeamID: "t1", Title: "a", CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := st.AddTask(store.Task{TeamID: "t1", Title: "b", CreatedAt: 1, UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}

	// The claim was decided while #1 waited for nothing.
	stale := first
	// Another member first makes #1 wait for #2.
	waiting := first
	waiting.After = []int{blocker.N}
	if ok, err := st.EditTask(first, waiting); err != nil || !ok {
		t.Fatalf("#1 waiting for #2 = %v, %v", ok, err)
	}

	claim := stale
	claim.Status, claim.Owner, claim.UpdatedAt = store.TaskClaimed, "backend", 2
	ok, err := st.UpdateTaskChecked(stale, claim, func(tasks []store.Task) error {
		return team.CanClaim(stale, "backend", tasks, false)
	})
	if err == nil && ok {
		t.Fatal("the claim went through a board where #1 waits for a task that isn't done")
	}
	if got, err := st.Task("t1", first.N); err != nil || got.Status == store.TaskClaimed ||
		!slices.Equal(got.After, []int{blocker.N}) {
		t.Fatalf("#1 = %+v, %v; want it open and waiting for #2", got, err)
	}
}
