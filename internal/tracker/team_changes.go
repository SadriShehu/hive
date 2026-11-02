package tracker

import (
	"fmt"
	"strings"

	"github.com/sadrishehu/hive/internal/gitx"
	"github.com/sadrishehu/hive/internal/store"
)

// ChangeReport is what a session or a worktree member changed.
type ChangeReport struct {
	Dir         string            // where the changes are
	Repo        string            // the repository whose refs the committed range reads
	Base        string            // the branch merged into, for a worktree member
	BaseRef     string            // the git ref behind Base
	Branch      string            // the member's branch, for a worktree member
	Worktree    bool              // a member's branch, not a shared folder
	Files       []gitx.FileChange // the paths, with their lines
	Uncommitted int               // paths with changes not committed
	Ahead       int               // commits ahead of the base or the upstream
	Diff        string            // unified diff: the branch's commits, then the uncommitted
	NotRepo     bool              // Dir isn't in a git repository
}

// Changes reads what a session in dir changed. When teamID and role name a
// member of a worktrees team, it reads the commits on that member's branch
// since the team's base, then the changes left in its worktree. Anything else
// is the folder's uncommitted changes and how far its branch is ahead of its
// upstream.
func (t *Tracker) Changes(dir, teamID, role string) (ChangeReport, error) {
	rep := ChangeReport{Dir: dir, Repo: dir}
	repoDir := dir
	var ref, branch string
	if teamID != "" && role != "" {
		tm, err := t.Store.TeamByID(teamID)
		if err != nil {
			return rep, err
		}
		if tm.Workspace == store.WorkspaceWorktrees {
			trees, err := t.Store.Worktrees(tm.ID)
			if err != nil {
				return rep, err
			}
			for _, w := range trees {
				if w.Role == role {
					rep.Dir, branch = w.Path, w.Branch
					break
				}
			}
			if branch != "" {
				ref, name, err := t.baseRef(tm)
				if err != nil {
					return rep, err
				}
				rep.Base, rep.BaseRef, rep.Branch, rep.Worktree = name, ref, branch, true
				repoDir = tm.Cwd // the branch lives in the team's folder, even when its HEAD is the base
				rep.Repo = repoDir
			}
		}
	}
	if !isDir(rep.Dir) {
		return rep, fmt.Errorf("%s is not there", rep.Dir)
	}
	if !gitx.IsRepo(rep.Dir) {
		rep.NotRepo = true
		return rep, nil
	}
	if rep.Worktree {
		files, diff, err := gitx.Committed(repoDir, ref, branch)
		if err != nil {
			return rep, err
		}
		rep.Files, rep.Diff = files, diff
		ahead, _, err := gitx.AheadBehind(repoDir, ref, branch)
		if err != nil {
			return rep, err
		}
		rep.Ahead = ahead
	}
	files, diff, err := gitx.UncommittedChanges(rep.Dir)
	if err != nil {
		return rep, err
	}
	rep.Files = mergeFiles(rep.Files, files)
	rep.Uncommitted = len(files)
	rep.Diff = joinDiff(rep.Diff, diff)
	if !rep.Worktree {
		rep.Ahead, _ = gitx.AheadOfUpstream(rep.Dir) // no upstream is 0, not an error
	}
	return rep, nil
}

// mergeFiles adds the uncommitted files to the committed ones, by path.
func mergeFiles(committed, uncommitted []gitx.FileChange) []gitx.FileChange {
	out := make([]gitx.FileChange, 0, len(committed)+len(uncommitted))
	at := map[string]int{}
	for _, f := range committed {
		at[f.Path] = len(out)
		out = append(out, f)
	}
	for _, f := range uncommitted {
		if i, ok := at[f.Path]; ok {
			out[i].Added += f.Added
			out[i].Removed += f.Removed
			if f.Status != "" {
				out[i].Status = f.Status
			}
			continue
		}
		at[f.Path] = len(out)
		out = append(out, f)
	}
	return out
}

// joinDiff puts the committed diff and the uncommitted one one after the
// other, with no doubled or missing newline.
func joinDiff(committed, uncommitted string) string {
	committed = strings.TrimRight(committed, "\n")
	uncommitted = strings.TrimRight(uncommitted, "\n")
	switch {
	case committed == "":
		return uncommitted
	case uncommitted == "":
		return committed
	}
	return committed + "\n" + uncommitted
}
