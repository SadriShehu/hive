package tui

import (
	"fmt"
	"strings"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tree"
)

// trashedTeamOf returns the team a node in the trash belongs to: the team's
// own node, or a member's.
func (m Model) trashedTeamOf(n *tree.Node) (store.Team, bool) {
	if n == nil || n.TeamID == "" {
		return store.Team{}, false
	}
	return m.trashedTeamByID(n.TeamID)
}

func (m Model) trashedTeamByID(id string) (store.Team, bool) {
	for _, t := range m.snap.Trashed {
		if t.ID == id {
			return t, true
		}
	}
	return store.Team{}, false
}

// trashCount words what the trash holds: "3 sessions, 1 team".
func (m Model) trashCount() string {
	s := fmt.Sprintf("%d sessions", len(m.trashed))
	switch n := len(m.snap.Trashed); n {
	case 0:
	case 1:
		s += ", 1 team"
	default:
		s += fmt.Sprintf(", %d teams", n)
	}
	return s
}

// membersText words a count of members: "1 member", "3 members".
func membersText(n int) string {
	if n == 1 {
		return "1 member"
	}
	return fmt.Sprintf("%d members", n)
}

func joinRoles(roles []string) string {
	switch len(roles) {
	case 0:
		return ""
	case 1:
		return roles[0]
	}
	return strings.Join(roles[:len(roles)-1], ", ") + " and " + roles[len(roles)-1]
}
