package tracker

import (
	"fmt"

	"github.com/sadrishehu/hive/internal/store"
)

// Done is what DoneTeam did.
type Done struct {
	Already   bool            // the team was done before
	Dropped   int             // queued messages that will never be delivered
	Running   []string        // roles whose agents still run
	Base      string          // what --clean took as merged into
	Worktrees []WorktreeState // with clean: each worktree, and whether it went
}

// DoneTeam marks a team's goal met. The team stays, with its history, and
// takes no more mail: what was queued is dropped. Nobody is stopped: the agent
// that runs it may well be the lead, and `hive team stop` asks first. With
// clean, the worktrees whose branches are merged go, with their branches;
// running it again on a done team cleans again.
func (t *Tracker) DoneTeam(tm store.Team, by string, clean bool) (Done, error) {
	d := Done{Already: tm.Status == store.TeamDone}
	if !d.Already {
		if err := t.Store.SetTeamStatus(tm.ID, store.TeamDone, t.World.Now()); err != nil {
			return d, err
		}
		n, err := t.Store.DropQueued(tm.ID)
		if err != nil {
			return d, err
		}
		d.Dropped = n
		text := "team done, by " + by + ": the goal is met, and no more mail is delivered"
		if n > 0 {
			text += "; dropped " + plural(n, "message") + " that waited"
		}
		if _, err := t.system(tm, text); err != nil {
			return d, err
		}
		tm.Status = store.TeamDone
	}
	var err error
	if d.Running, err = t.runningRoles(tm); err != nil {
		return d, err
	}
	if clean && tm.Workspace == store.WorkspaceWorktrees {
		d.Worktrees, d.Base, err = t.cleanWorktrees(tm, d.Running)
	}
	return d, err
}

// runningRoles returns the roles of a team whose agents run, retired ones
// too, as of a fresh look at the processes: a member that hasn't reported in
// runs as long as its pane is there.
func (t *Tracker) runningRoles(tm store.Team) ([]string, error) {
	t.World.Forget()
	if _, err := t.Refresh(); err != nil {
		return nil, err
	}
	members, err := t.Store.Members(tm.ID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range members {
		if m.SessionID == "" {
			if t.inLaunchedPane(m) {
				out = append(out, m.Role)
			}
			continue
		}
		if s, ok, err := t.Store.Get(m.SessionID); err != nil {
			return nil, err
		} else if ok && s.Live() {
			out = append(out, m.Role)
		}
	}
	return out, nil
}

// closed says why a team takes no more mail, or returns nil.
func closed(tm store.Team) error {
	if tm.Status == store.TeamDone {
		return fmt.Errorf("%s is done; it takes no more messages, members or tasks", tm.Name)
	}
	return nil
}
