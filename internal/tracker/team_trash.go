package tracker

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/sadrishehu/hive/internal/store"
)

// TeamRunning returns the roles of a team whose agents run, as of a fresh
// look at the processes: a member that hasn't reported in runs as long as
// the pane hive opened for it is still there.
func (t *Tracker) TeamRunning(tm store.Team) ([]string, error) {
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
		if !m.Active() {
			continue
		}
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

// onAnotherTeam reports whether a session plays a role on a team that isn't
// in the trash: such a session stays when its other team is trashed.
func (t *Tracker) onAnotherTeam(sessionID, teamID string) (bool, error) {
	ms, err := t.Store.Memberships(sessionID)
	if err != nil {
		return false, err
	}
	for _, m := range ms {
		if m.TeamID == teamID {
			continue
		}
		trashed, err := t.Store.TeamTrashed(m.TeamID)
		if err != nil {
			return false, err
		}
		if !trashed {
			return true, nil
		}
	}
	return false, nil
}

// addIDs appends the sessions in family to ids, once each.
func addIDs(ids []string, family []store.Session) []string {
	for _, s := range family {
		if !slices.Contains(ids, s.ID) {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

// TrashTeam moves a team and the sessions of its members to the trash, once
// no member runs. A member's session that still plays a role on a team out
// of the trash stays, so one agent on two teams isn't taken away from the
// other. The tools keep their copies, and RestoreTeam brings it all back.
func (t *Tracker) TrashTeam(tm store.Team) ([]store.Session, error) {
	if running, err := t.TeamRunning(tm); err != nil {
		return nil, err
	} else if len(running) > 0 {
		return nil, fmt.Errorf("%s still has %s running; stop them first", tm.Name, joinRoles(running))
	}
	members, err := t.Store.Members(tm.ID)
	if err != nil {
		return nil, err
	}
	var family []store.Session
	var ids []string
	for _, m := range members {
		if !m.Active() || m.SessionID == "" {
			continue
		}
		shared, err := t.onAnotherTeam(m.SessionID, tm.ID)
		if err != nil {
			return nil, err
		}
		if shared {
			continue
		}
		f, err := t.Store.Family(m.SessionID, false)
		if err != nil {
			return nil, err
		}
		family = append(family, f...)
		ids = addIDs(ids, f)
	}
	at := t.World.Now()
	if err := t.Store.TrashTeam(tm.ID, at); err != nil {
		return nil, err
	}
	if err := t.Store.Trash(ids, at); err != nil {
		return nil, errors.Join(err, t.Store.RestoreTeam(tm.ID))
	}
	return family, nil
}

// RestoreTeam takes a team and its members' sessions out of the trash.
func (t *Tracker) RestoreTeam(tm store.Team) ([]store.Session, error) {
	members, err := t.Store.Members(tm.ID)
	if err != nil {
		return nil, err
	}
	var family []store.Session
	var ids []string
	for _, m := range members {
		if m.SessionID == "" {
			continue
		}
		f, err := t.Store.Family(m.SessionID, true)
		if err != nil {
			return nil, err
		}
		family = append(family, f...)
		ids = addIDs(ids, f)
	}
	if err := t.Store.Restore(ids); err != nil {
		return nil, err
	}
	return family, t.Store.RestoreTeam(tm.ID)
}

// PurgeTeam deletes a team in the trash for good: its members' sessions, each
// through its tool, then the team. It stops at the first session its tool
// fails to delete, which stays in the trash with the team.
func (t *Tracker) PurgeTeam(ctx context.Context, tm store.Team) (Purged, error) {
	var out Purged
	members, err := t.Store.Members(tm.ID)
	if err != nil {
		return out, err
	}
	for _, m := range members {
		if m.SessionID == "" {
			continue
		}
		s, ok, err := t.Store.Get(m.SessionID)
		if err != nil {
			return out, err
		}
		if !ok {
			continue
		}
		if f, err := t.Store.Family(s.ID, true); err != nil {
			return out, err
		} else if len(f) == 0 {
			continue // it stayed for another team
		}
		p, err := t.Purge(ctx, s)
		out.Sessions = append(out.Sessions, p.Sessions...)
		for _, tool := range p.Kept {
			if !slices.Contains(out.Kept, tool) {
				out.Kept = append(out.Kept, tool)
			}
		}
		if err != nil {
			return out, err
		}
	}
	return out, t.Store.DeleteTeam(tm.ID)
}

func joinRoles(roles []string) string {
	switch len(roles) {
	case 0:
		return ""
	case 1:
		return roles[0]
	}
	out := ""
	for i, r := range roles {
		switch {
		case i == 0:
			out = r
		case i == len(roles)-1:
			out += " and " + r
		default:
			out += ", " + r
		}
	}
	return out
}
