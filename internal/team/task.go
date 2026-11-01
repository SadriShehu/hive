package team

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/sadrishehu/hive/internal/store"
)

// ParseTask reads a task number: "4" or "#4".
func ParseTask(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%q isn't a task number; tasks are #1, #2, …", s)
	}
	return n, nil
}

// ParseAfter reads the tasks one waits for: "1,2", "#1 #2" or "1, 2".
func ParseAfter(s string) ([]int, error) {
	out := []int{}
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		n, err := ParseTask(f)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// CheckAfter says why a new task can't wait for after: a task the board
// doesn't have. A new task only waits for tasks already on the board, so
// they can never wait for each other in a ring.
func CheckAfter(after []int, tasks []store.Task) error {
	for _, n := range after {
		if !slices.ContainsFunc(tasks, func(t store.Task) bool { return t.N == n }) {
			return fmt.Errorf("the board has no #%d to wait for", n)
		}
	}
	return nil
}

// Cycles reports whether making task n wait for after would make it wait,
// directly or through other tasks, for itself.
func Cycles(n int, after []int, tasks []store.Task) bool {
	seen := map[int]bool{}
	var waitsFor func(int) bool
	waitsFor = func(task int) bool {
		if task == n {
			return true
		}
		if seen[task] {
			return false
		}
		seen[task] = true
		for _, t := range tasks {
			if t.N != task {
				continue
			}
			for _, next := range t.After {
				if waitsFor(next) {
					return true
				}
			}
			break
		}
		return false
	}
	for _, task := range after {
		if waitsFor(task) {
			return true
		}
	}
	return false
}

// Waiting lists the tasks t waits for that aren't done.
func Waiting(t store.Task, tasks []store.Task) []int {
	var out []int
	for _, n := range t.After {
		i := slices.IndexFunc(tasks, func(o store.Task) bool { return o.N == n })
		if i < 0 || tasks[i].Status != store.TaskDone {
			out = append(out, n)
		}
	}
	return out
}

// Ready reports whether t can be taken: it is open and everything it waits
// for is done.
func Ready(t store.Task, tasks []store.Task) bool {
	return t.Status == store.TaskOpen && len(Waiting(t, tasks)) == 0
}

// Unblocked lists the open tasks that closing n made ready: they wait for n,
// and for nothing else that isn't done.
func Unblocked(n int, tasks []store.Task) []store.Task {
	var out []store.Task
	for _, t := range tasks {
		if slices.Contains(t.After, n) && Ready(t, tasks) {
			out = append(out, t)
		}
	}
	return out
}

// CanClaim says why role can't claim t, or returns nil. A task that is done
// can't be claimed; one claimed or owned by another role, or waiting for
// tasks that aren't done, needs force.
func CanClaim(t store.Task, role string, tasks []store.Task, force bool) error {
	switch {
	case t.Status == store.TaskDone:
		return fmt.Errorf("#%d is done", t.N)
	case force:
		return nil
	case t.Status == store.TaskClaimed && t.Owner != role:
		return fmt.Errorf("#%d is claimed by %s; --force takes it over", t.N, t.Owner)
	case t.Owner != "" && t.Owner != role:
		return fmt.Errorf("#%d is %s's; --force takes it", t.N, t.Owner)
	}
	if w := Waiting(t, tasks); len(w) > 0 {
		return fmt.Errorf("#%d waits for %s; --force claims it anyway", t.N, Numbers(w))
	}
	return nil
}

// Numbers lists task numbers as "#4 #5".
func Numbers(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = "#" + strconv.Itoa(n)
	}
	return strings.Join(parts, " ")
}

// Held returns the task role is working on: the lowest-numbered one it has
// claimed.
func Held(role string, tasks []store.Task) (store.Task, bool) {
	for _, t := range tasks {
		if t.Status == store.TaskClaimed && t.Owner == role {
			return t, true
		}
	}
	return store.Task{}, false
}

// Counts is how many of a board's tasks stand where.
type Counts struct {
	Total   int `json:"total"`
	Open    int `json:"open"`
	Claimed int `json:"claimed"`
	Done    int `json:"done"`
	Blocked int `json:"blocked"`
}

// Count counts tasks by status.
func Count(tasks []store.Task) Counts {
	var c Counts
	for _, t := range tasks {
		c.Total++
		switch t.Status {
		case store.TaskOpen:
			c.Open++
		case store.TaskClaimed:
			c.Claimed++
		case store.TaskDone:
			c.Done++
		case store.TaskBlocked:
			c.Blocked++
		}
	}
	return c
}

// TaskEvent words what happened to a task, as hive posts it: "#4 done by
// backend: what changed". Every one starts with the task's number; by can be
// "" when nobody is known.
func TaskEvent(t store.Task, by string) string {
	if by != "" {
		by = " by " + by
	}
	switch t.Status {
	case store.TaskClaimed:
		return fmt.Sprintf("#%d claimed%s: %s", t.N, by, t.Title)
	case store.TaskDone:
		if t.Note == "" {
			return fmt.Sprintf("#%d done%s: %s", t.N, by, t.Title)
		}
		return fmt.Sprintf("#%d done%s: %s", t.N, by, t.Note)
	case store.TaskBlocked:
		return fmt.Sprintf("#%d blocked%s: %s", t.N, by, t.Note)
	}
	s := fmt.Sprintf("#%d added%s", t.N, by)
	if t.Owner != "" {
		s += " for " + t.Owner
	}
	return s + ": " + t.Title
}

// ReadyList words tasks that became ready, with their owners: "#5 (frontend), #6".
func ReadyList(tasks []store.Task) string {
	parts := make([]string, len(tasks))
	for i, t := range tasks {
		parts[i] = fmt.Sprintf("#%d", t.N)
		if t.Owner != "" {
			parts[i] += " (" + t.Owner + ")"
		}
	}
	return strings.Join(parts, ", ")
}

// ReadyNote is what hive tells the owner of a task that is ready, and what
// made it so ("" when it was ready as it was added).
func ReadyNote(t store.Task, because string) string {
	s := fmt.Sprintf("#%d %s is yours, and ready", t.N, t.Title)
	if because != "" {
		s += " (" + because + ")"
	}
	s += "."
	if t.Body != "" {
		s += " " + t.Body
	}
	return s + fmt.Sprintf(" `hive team task claim %d` takes it.", t.N)
}
