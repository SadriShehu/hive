package tracker

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
)

// onTeam reports whether role is an active member of the team.
func onTeam(members []store.Member, role string) bool {
	return slices.ContainsFunc(members, func(m store.Member) bool { return m.Role == role && m.Active() })
}

// board reads a team afresh, with its members and tasks: a team passed in
// may be from before a pause or a change of lead.
func (t *Tracker) board(teamID string) (store.Team, []store.Member, []store.Task, error) {
	tm, err := t.Store.TeamByID(teamID)
	if err != nil {
		return tm, nil, nil, err
	}
	members, err := t.Store.Members(tm.ID)
	if err != nil {
		return tm, nil, nil, err
	}
	tasks, err := t.Store.Tasks(tm.ID)
	return tm, members, tasks, err
}

func findTask(tasks []store.Task, n int) (int, error) {
	i := slices.IndexFunc(tasks, func(t store.Task) bool { return t.N == n })
	if i < 0 {
		return -1, fmt.Errorf("the board has no #%d; `hive team task ls` lists the tasks", n)
	}
	return i, nil
}

// AddTask puts a task on a team's board, added by by. An owner whose task is
// ready as it is added is told so.
func (t *Tracker) AddTask(tm store.Team, by, title, body, owner string, after []int) (store.Task, []Sent, error) {
	title = strings.TrimSpace(team.Clean(title))
	if title == "" {
		return store.Task{}, nil, errors.New("a task needs a title")
	}
	tm, members, tasks, err := t.board(tm.ID)
	if err != nil {
		return store.Task{}, nil, err
	}
	if err := closed(tm); err != nil {
		return store.Task{}, nil, err
	}
	owner = strings.TrimPrefix(strings.TrimSpace(owner), "@")
	if owner != "" && !onTeam(members, owner) {
		return store.Task{}, nil, fmt.Errorf("the team has no %s; roles: %s", owner, team.Roster(members))
	}
	if err := team.CheckAfter(after, tasks); err != nil {
		return store.Task{}, nil, err
	}
	now := t.World.Now()
	task, err := t.Store.AddTask(store.Task{TeamID: tm.ID, Title: title, Body: strings.TrimSpace(team.Clean(body)), Owner: owner,
		After: after, CreatedBy: by, CreatedAt: now, UpdatedAt: now})
	if err != nil || owner == "" || owner == by || !team.Ready(task, tasks) {
		return task, nil, err
	}
	sent, err := t.tell(tm, members, owner, team.ReadyNote(task, ""))
	return task, sent, err
}

// EditTask changes a task's title, body, owner and the tasks it waits for,
// keeping its number and status. The owner must be on the team, and after
// must name tasks already on the board, as when a task is added. It loses,
// naming who moved it, when another member changed the task meanwhile.
func (t *Tracker) EditTask(tm store.Team, n int, title, body, owner string, after []int) (store.Task, error) {
	title = strings.TrimSpace(team.Clean(title))
	if title == "" {
		return store.Task{}, errors.New("a task needs a title")
	}
	tm, members, tasks, err := t.board(tm.ID)
	if err != nil {
		return store.Task{}, err
	}
	i, err := findTask(tasks, n)
	if err != nil {
		return store.Task{}, err
	}
	owner = strings.TrimPrefix(strings.TrimSpace(owner), "@")
	if owner != "" && !onTeam(members, owner) {
		return store.Task{}, fmt.Errorf("the team has no %s; roles: %s", owner, team.Roster(members))
	}
	if err := team.CheckAfter(after, tasks); err != nil {
		return store.Task{}, err
	}
	if slices.Contains(after, n) {
		return store.Task{}, fmt.Errorf("#%d can't wait for itself", n)
	}
	if team.Cycles(n, after, tasks) {
		return store.Task{}, fmt.Errorf("#%d can't wait for a task that waits for it", n)
	}
	if after == nil {
		after = []int{}
	}
	old := tasks[i]
	task := old
	task.Title, task.Body, task.Owner, task.After = title, strings.TrimSpace(team.Clean(body)), owner, after
	if task.Status == store.TaskClaimed && task.Owner == "" {
		task.Status, task.Note = store.TaskOpen, ""
	}
	task.UpdatedAt = t.World.Now()
	ok, err := t.Store.EditTask(old, task)
	if err != nil {
		return store.Task{}, err
	}
	if !ok {
		return store.Task{}, t.stale(old)
	}
	return task, nil
}

// ClaimTask makes a task by's to work on. It is refused on a paused team,
// and, unless force, for a task that waits for tasks that aren't done or is
// another member's.
func (t *Tracker) ClaimTask(tm store.Team, by string, n int, force bool) (store.Task, error) {
	tm, members, tasks, err := t.board(tm.ID)
	if err != nil {
		return store.Task{}, err
	}
	if err := closed(tm); err != nil {
		return store.Task{}, err
	}
	if tm.Paused {
		return store.Task{}, fmt.Errorf("%s is paused: no tasks are claimed until it resumes", tm.Name)
	}
	if !onTeam(members, by) {
		return store.Task{}, fmt.Errorf("tasks on %s are claimed by its members; roles: %s (--as <role> claims for one)",
			tm.Name, team.Roster(members))
	}
	i, err := findTask(tasks, n)
	if err != nil {
		return store.Task{}, err
	}
	old := tasks[i]
	if old.Status == store.TaskClaimed && old.Owner == by {
		return old, nil
	}
	if err := team.CanClaim(old, by, tasks, force); err != nil {
		return store.Task{}, err
	}
	task := old
	task.Status, task.Owner, task.Note, task.UpdatedAt = store.TaskClaimed, by, "", t.World.Now()
	if err := t.moveTask(old, task); err != nil {
		return store.Task{}, err
	}
	// Taken over with --force: the member that held it hears of it.
	var to []string
	if old.Owner != "" && old.Owner != by && onTeam(members, old.Owner) {
		to = []string{old.Owner}
	}
	_, err = t.announce(tm, members, team.TaskEvent(task, by), to, false)
	return task, err
}

// DoneTask closes a task, with a note on what changed. hive posts it, to the
// lead when an agent leads, and tells the owner of each task it made ready.
// It returns the tasks that became ready.
func (t *Tracker) DoneTask(tm store.Team, by string, n int, note string) (store.Task, []store.Task, []Sent, error) {
	note = team.Clean(note)
	tm, members, tasks, err := t.board(tm.ID)
	if err != nil {
		return store.Task{}, nil, nil, err
	}
	i, err := findTask(tasks, n)
	if err != nil {
		return store.Task{}, nil, nil, err
	}
	old := tasks[i]
	if old.Status == store.TaskDone {
		return store.Task{}, nil, nil, fmt.Errorf("#%d is done already", n)
	}
	task := old
	task.Status, task.Note, task.UpdatedAt = store.TaskDone, strings.TrimSpace(note), t.World.Now()
	if task.Owner == "" && onTeam(members, by) {
		task.Owner = by
	}
	if err := t.moveTask(old, task); err != nil {
		return store.Task{}, nil, nil, err
	}
	tasks[i] = task
	ready := team.Unblocked(n, tasks)
	event := team.TaskEvent(task, by)
	body := event
	if len(ready) > 0 {
		body += " · ready: " + team.ReadyList(ready)
	}
	lead := t.toLead(tm, members, by)
	sent, err := t.announce(tm, members, body, lead, false)
	errs := []error{err}
	for _, r := range ready {
		// The one who closed it reads the ready tasks in its own output, and
		// the lead in the post.
		if r.Owner == "" || r.Owner == by || slices.Contains(lead, r.Owner) || !onTeam(members, r.Owner) {
			continue
		}
		s, err := t.tell(tm, members, r.Owner, team.ReadyNote(r, event))
		sent = append(sent, s...)
		errs = append(errs, err)
	}
	return task, ready, sent, errors.Join(errs...)
}

// BlockTask marks a task blocked, saying why. hive posts it, to the lead
// when an agent leads.
func (t *Tracker) BlockTask(tm store.Team, by string, n int, why string) (store.Task, []Sent, error) {
	why = strings.TrimSpace(team.Clean(why))
	if why == "" {
		return store.Task{}, nil, errors.New("say why it is blocked: --why \"…\"")
	}
	tm, members, tasks, err := t.board(tm.ID)
	if err != nil {
		return store.Task{}, nil, err
	}
	i, err := findTask(tasks, n)
	if err != nil {
		return store.Task{}, nil, err
	}
	old := tasks[i]
	if old.Status == store.TaskDone {
		return store.Task{}, nil, fmt.Errorf("#%d is done", n)
	}
	task := old
	task.Status, task.Note, task.UpdatedAt = store.TaskBlocked, why, t.World.Now()
	if task.Owner == "" && onTeam(members, by) {
		task.Owner = by
	}
	if err := t.moveTask(old, task); err != nil {
		return store.Task{}, nil, err
	}
	sent, err := t.announce(tm, members, team.TaskEvent(task, by), t.toLead(tm, members, by), false)
	return task, sent, err
}

// moveTask stores a task's move, unless another member moved it first.
func (t *Tracker) moveTask(old, task store.Task) error {
	ok, err := t.Store.UpdateTask(old, task)
	if err != nil || ok {
		return err
	}
	return t.stale(old)
}

// stale says why a task as it is now replaced the one read: somebody moved it.
func (t *Tracker) stale(old store.Task) error {
	now, err := t.Store.Task(old.TeamID, old.N)
	if err != nil {
		return err
	}
	who := ""
	if now.Owner != "" {
		who = " by " + now.Owner
	}
	return fmt.Errorf("#%d changed meanwhile: it is %s%s now", old.N, now.Status, who)
}

// toLead is who a task's news goes to besides the channel: the lead, when an
// agent leads and didn't make the news itself.
func (t *Tracker) toLead(tm store.Team, members []store.Member, by string) []string {
	if tm.LeadRole == "" || tm.LeadRole == by || !onTeam(members, tm.LeadRole) {
		return nil
	}
	return []string{tm.LeadRole}
}

// announce adds hive's post about a task to the channel, queued for roles,
// and delivers it to them. A post for one role is shown as to that role.
func (t *Tracker) announce(tm store.Team, members []store.Member, body string, roles []string, private bool) ([]Sent, error) {
	if tm.Status == store.TeamDone {
		return nil, nil // a done team takes no new post or mail
	}
	to := ""
	if len(roles) == 1 {
		to = roles[0]
	}
	if _, err := t.Store.AddMessage(store.Message{TeamID: tm.ID, From: store.FromHive, To: to, Body: body,
		Kind: store.MsgSystem, Private: private, At: t.World.Now()}, roles); err != nil {
		return nil, err
	}
	return t.deliverAll(tm, members, roles), nil
}

// tell writes to one member for hive, out of the user's feed: the channel
// already says what happened.
func (t *Tracker) tell(tm store.Team, members []store.Member, role, body string) ([]Sent, error) {
	return t.announce(tm, members, body, []string{role}, true)
}

// WaitOptions says what `hive team wait` waits for. With none set, it waits
// for any task to close or a message for the caller.
type WaitOptions struct {
	Task int           // this task closes: it is done or blocked
	From string        // a message from this role (or you, or hive) arrives for the caller
	Any  bool          // anything on the board: a task added or moved, or someone writing in the channel
	Poll time.Duration // how often to look again
}

// boardMark is the board as a wait found it, to tell what changed since.
type boardMark struct {
	tasks   map[int]store.Task
	lastMsg int64
}

func (t *Tracker) markBoard(teamID string) (boardMark, error) {
	mark := boardMark{tasks: map[int]store.Task{}}
	tasks, err := t.Store.Tasks(teamID)
	if err != nil {
		return mark, err
	}
	for _, task := range tasks {
		mark.tasks[task.N] = task
	}
	mark.lastMsg, err = t.Store.LastMessageID(teamID)
	return mark, err
}

// WaitTeam blocks until what o asks for happens on a team's board, and
// returns what happened as lines to print. role is the caller's role on the
// team, "" when it has none. Messages queued for the role that it returns
// are marked delivered, as `hive team inbox` does, so they aren't typed into
// the caller's pane again; a paused team keeps them. ctx bounds the wait.
func (t *Tracker) WaitTeam(ctx context.Context, tm store.Team, role string, o WaitOptions) ([]string, error) {
	tm, members, tasks, err := t.board(tm.ID)
	if err != nil {
		return nil, err
	}
	if o.Task > 0 {
		if _, err := findTask(tasks, o.Task); err != nil {
			return nil, err
		}
	}
	if o.From != "" && o.From != store.FromYou && o.From != store.FromHive &&
		!slices.ContainsFunc(members, func(m store.Member) bool { return m.Role == o.From }) {
		return nil, fmt.Errorf("the team has no %s; roles: %s", o.From, team.Roster(members))
	}
	mark, err := t.markBoard(tm.ID)
	if err != nil {
		return nil, err
	}
	for {
		lines, err := t.boardNews(tm.ID, role, o, mark)
		if err != nil || len(lines) > 0 {
			return lines, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(o.Poll):
		}
		t.World.Forget()
	}
}

// boardNews is what happened since mark that a wait with o returns on.
func (t *Tracker) boardNews(teamID, role string, o WaitOptions, mark boardMark) ([]string, error) {
	tm, _, tasks, err := t.board(teamID)
	if err != nil {
		return nil, err
	}
	plain := o.Task == 0 && o.From == "" && !o.Any // a task closes, or a message comes
	type taskNews struct {
		n    int
		line string
	}
	var news []taskNews
	for _, task := range tasks {
		old, seen := mark.tasks[task.N]
		moved := !seen || old.Status != task.Status || old.Owner != task.Owner || old.Note != task.Note
		if (o.Task == task.N && task.Closed()) || (o.Any && moved) || (plain && moved && task.Closed()) {
			by := task.Owner
			if task.Status == store.TaskOpen {
				by = task.CreatedBy
			}
			news = append(news, taskNews{task.N, team.TaskEvent(task, by)})
		}
	}

	var msgs []store.Message
	if o.Any || o.From != "" || o.Task == 0 {
		wanted := func(m store.Message) bool { return o.Any || o.From == "" || m.From == o.From }
		taken := map[int64]bool{}
		if role != "" && !tm.Paused {
			pending, err := t.Store.Pending(tm.ID, role)
			if err != nil {
				return nil, err
			}
			pending = slices.DeleteFunc(pending, func(m store.Message) bool { return !wanted(m) })
			if pending, err = t.Store.ClaimMessages(role, pending, t.World.Now()); err != nil {
				return nil, err
			}
			for _, m := range pending {
				taken[m.ID] = true
			}
			msgs = pending
		}
		// What others write in the channel: everything, for a caller off the
		// team; the rest of the channel, with --any. hive's own posts are
		// left out: they say what the tasks above say.
		if role == "" || o.Any {
			since, err := t.Store.MessagesAfter(tm.ID, mark.lastMsg)
			if err != nil {
				return nil, err
			}
			for _, m := range since {
				if !taken[m.ID] && m.Kind != store.MsgSystem && (role == "" || m.From != role) && wanted(m) {
					msgs = append(msgs, m)
				}
			}
			slices.SortFunc(msgs, func(a, b store.Message) int { return cmp.Compare(a.ID, b.ID) })
		}
	}

	// A message from hive about a task says more than the task's own line.
	var lines []string
	for _, n := range news {
		if !slices.ContainsFunc(msgs, func(m store.Message) bool {
			return m.From == store.FromHive && strings.HasPrefix(m.Body, fmt.Sprintf("#%d ", n.n))
		}) {
			lines = append(lines, n.line)
		}
	}
	for _, m := range msgs {
		from := m.From
		if m.To != "" && m.To != role {
			from += " → " + m.To
		}
		lines = append(lines, team.Envelope(tm.Name, from, m.Body))
	}
	return lines, nil
}
