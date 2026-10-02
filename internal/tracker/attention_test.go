package tracker

import (
	"fmt"
	"slices"
	"testing"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

// isDone checks what Done says of id.
func isDone(t *testing.T, tr *Tracker, id, wantState string, wantDone bool) {
	t.Helper()
	state, done, err := tr.Done(get(t, tr, id))
	if err != nil || done != wantDone || state != wantState {
		t.Fatalf("Done(%s) = %q, %v, %v; want %q, %v", id, state, done, err, wantState, wantDone)
	}
}

func TestDoneOnlyOnceTheMessageIsAnswered(t *testing.T) {
	tr, w := newWorld(t)
	claudeHook(t, tr, w, 100, "A", agent.Start)
	isDone(t, tr, "claude:A", store.StatusIdle, true) // given nothing, an idle agent is done

	// hive types a message; the agent hasn't picked it up yet.
	w.now = 2_000
	if err := tr.Store.SetInput("claude:A", 2_000); err != nil {
		t.Fatal(err)
	}
	isDone(t, tr, "claude:A", "", false)
	for _, step := range []struct {
		typ   agent.EventType
		state string
		done  bool
	}{
		{agent.Prompt, "", false},
		{agent.Busy, "", false},
		{agent.Attention, store.StatusAttention, true}, // it asks the user: that answers too
		{agent.Busy, "", false},                        // allowed; back to work
		{agent.Idle, store.StatusIdle, true},
	} {
		w.now += 100
		claudeHook(t, tr, w, 100, "A", step.typ)
		isDone(t, tr, "claude:A", step.state, step.done)
	}

	claudeHook(t, tr, w, 100, "A", agent.End)
	isDone(t, tr, "claude:A", store.StatusExited, true)
}

func TestStartingUpDoesNotAnswerTheFirstPrompt(t *testing.T) {
	tr, w := newWorld(t)
	// hive started opencode with a prompt in pane %2; until it names its
	// session, the pane holds the prompt.
	w.add(401, 95, "opencode --prompt port the tests")
	if err := tr.Store.SetInput("%2", 900); err != nil {
		t.Fatal(err)
	}
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "O", Type: agent.Start, PID: 401})
	if o := get(t, tr, "opencode:O"); o.Pane != "%2" || o.Status != store.StatusIdle {
		t.Fatalf("O = %+v, want idle in %%2", o)
	}
	isDone(t, tr, "opencode:O", "", false)

	w.now = 2_000
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "O", Type: agent.Busy, PID: 401})
	w.now = 3_000
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "O", Type: agent.Idle, PID: 401})
	isDone(t, tr, "opencode:O", store.StatusIdle, true)
}

func TestAPromptRecordedAfterStartMovesWithTheNextEvent(t *testing.T) {
	tr, w := newWorld(t)
	w.add(401, 95, "opencode --prompt port the tests")
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "O", Type: agent.Start, PID: 401})
	// The agent reported in before hive got to write down the prompt.
	if err := tr.Store.SetInput("%2", 900); err != nil {
		t.Fatal(err)
	}
	w.now = 2_000
	ingest(t, tr, agent.Event{Tool: "opencode", SessionID: "O", Type: agent.Busy, PID: 401})
	if _, pending, _ := tr.Store.Input("opencode:O"); !pending {
		t.Fatal("the pane's prompt didn't move to the session reporting from it")
	}
}

func TestDoneWaitsForTheEndOfSessionsWithoutStatus(t *testing.T) {
	tr, _ := newWorld(t)
	sessions, err := tr.Refresh() // Claude A (pid 100) has never reported in
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(sessions, func(s store.Session) bool { return s.PID == 100 })
	if i < 0 {
		t.Fatal("no untracked session for pid 100")
	}
	if state, done, _ := tr.Done(sessions[i]); done {
		t.Fatalf("an untracked agent is done (%s); hive can't see its turns", state)
	}
}

func TestOnAttentionHearsEachNewWait(t *testing.T) {
	tr, w := newWorld(t)
	var told []string
	tr.OnAttention = func(s store.Session) { told = append(told, fmt.Sprintf("%s@%d", s.ID, s.StatusAt)) }

	claudeHook(t, tr, w, 100, "A", agent.Start)
	w.now = 2_000
	claudeHook(t, tr, w, 100, "A", agent.Attention)
	w.now = 2_100
	claudeHook(t, tr, w, 100, "A", agent.Attention) // still the same wait
	w.now = 2_200
	claudeHook(t, tr, w, 100, "A", agent.Busy)
	w.now = 2_300
	claudeHook(t, tr, w, 100, "A", agent.Attention)
	w.now = 2_400
	claudeHook(t, tr, w, 100, "A", agent.Idle)
	// A late event from before the agent went idle is old news.
	ingest(t, tr, agent.Event{Tool: "claude", SessionID: "A", Type: agent.Attention, At: 2_350})

	if want := []string{"claude:A@2000", "claude:A@2300"}; !slices.Equal(told, want) {
		t.Fatalf("told %v, want %v", told, want)
	}
}

func TestNeedsYouAndPaneFor(t *testing.T) {
	sessions := []store.Session{
		{ID: "claude:A", Kind: store.KindInteractive, Status: store.StatusAttention, StatusAt: 300, PID: 1, Pane: "%1"},
		{ID: "claude:A/sub", ParentID: "claude:A", Kind: store.KindInternal, Status: store.StatusAttention, StatusAt: 200},
		{ID: "opencode:B", Kind: store.KindHeadless, Status: store.StatusWorking, StatusAt: 100, PID: 2},
		{ID: "codex:C", Kind: store.KindInteractive, Status: store.StatusAttention, StatusAt: 50}, // ended
	}
	var ids []string
	for _, s := range NeedsYou(sessions) {
		ids = append(ids, s.ID)
	}
	if want := []string{"claude:A/sub", "claude:A"}; !slices.Equal(ids, want) {
		t.Fatalf("NeedsYou = %v, want %v: live, longest waiting first", ids, want)
	}

	get := func(id string) (store.Session, bool) {
		i := slices.IndexFunc(sessions, func(s store.Session) bool { return s.ID == id })
		if i < 0 {
			return store.Session{}, false
		}
		return sessions[i], true
	}
	if pane := PaneFor(sessions[1], get); pane != "%1" {
		t.Errorf("a subagent's pane = %q, want its parent's", pane)
	}
	if pane := PaneFor(sessions[2], get); pane != "" {
		t.Errorf("a headless run's pane = %q, want none", pane)
	}
}
