package team

import (
	"strings"

	"github.com/sadrishehu/hive/internal/store"
)

// Branch is the git branch a member of a worktrees team works on.
func Branch(team, role string) string { return "hive/" + team + "/" + role }

// Folder is where a member works: its worktree, or the team's folder.
func Folder(t store.Team, m store.Member) string {
	if m.Cwd != "" {
		return m.Cwd
	}
	return t.Cwd
}

// workLine is the brief's line on where the member works and how its work
// reaches the others. On a worktrees team the lead stays in the team's
// folder, so that what it merges lands on the branch the folder has.
func workLine(t store.Team, m store.Member, members []store.Member) string {
	if t.Workspace != store.WorkspaceWorktrees {
		return "- Everyone works in the same folder. Pull before you start; commit small.\n"
	}
	if t.LeadRole != "" && m.Role == t.LeadRole {
		var branches []string
		for _, o := range members {
			if o.Active() && o.Role != t.LeadRole {
				branches = append(branches, Branch(t.Name, o.Role))
			}
		}
		line := "- You work in the team's folder. Each teammate works in a git worktree of its own, on its own\n  branch"
		if len(branches) > 0 {
			line += " (" + strings.Join(branches, ", ") + ")"
		}
		return line + ". Merge their branches here as their parts are done.\n"
	}
	merger := "the lead"
	if t.LeadRole == "" {
		merger = "the human"
	}
	return "- You work in a git worktree of your own, on the branch " + Branch(t.Name, m.Role) + ". Commit small,\n" +
		"  and commit before you say a part is done: " + merger + " merges your branch.\n"
}
