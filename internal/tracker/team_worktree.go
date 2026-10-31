package tracker

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
)

// WorktreeDir is where the worktree of a team's member goes.
func WorktreeDir(teamName, role string) string {
	return filepath.Join(paths.DataDir(), "worktrees", teamName, role)
}

// git runs git in dir and returns what it printed, trimmed. Its error is
// what git said, without the hints.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		for line := range strings.SplitSeq(stderr.String(), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "hint:") {
				line = strings.TrimPrefix(strings.TrimPrefix(line, "fatal: "), "error: ")
				return "", fmt.Errorf("git %s: %s", args[0], line)
			}
		}
		return "", fmt.Errorf("git %s: %w", args[0], err)
	}
	return strings.TrimSpace(out.String()), nil
}

// repoBase checks that a team called name can give its members worktrees of
// the repository dir is in, and returns where they start from: the branch
// dir has, and its commit. It runs before anything is recorded or started.
func (t *Tracker) repoBase(name, dir string) (store.TeamBase, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return store.TeamBase{}, errors.New("--worktrees needs git, which isn't on PATH")
	}
	if _, err := git(dir, "rev-parse", "--show-toplevel"); err != nil {
		return store.TeamBase{}, fmt.Errorf("--worktrees needs a git repository, and %s isn't in one", dir)
	}
	head, err := git(dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return store.TeamBase{}, fmt.Errorf("--worktrees makes the worktrees from a commit, and the repository at %s has none yet", dir)
	}
	if _, err := git(dir, "check-ref-format", "refs/heads/"+team.Branch(name, "role")); err != nil {
		return store.TeamBase{}, fmt.Errorf("git can't take %q in a branch name; pick another team name for --worktrees", name)
	}
	branch, _ := git(dir, "symbolic-ref", "--quiet", "--short", "HEAD") // "" when detached
	return store.TeamBase{Branch: branch, Head: head, CreatedAt: t.World.Now()}, nil
}

// startWorktrees records where a worktrees team starts from, and says so in
// its channel.
func (t *Tracker) startWorktrees(tm store.Team, base store.TeamBase) error {
	base.TeamID = tm.ID
	if err := t.Store.PutTeamBase(base); err != nil {
		return err
	}
	from := base.Branch
	if from == "" {
		from = "a detached HEAD"
	}
	text := fmt.Sprintf("each member works in a worktree of its own, on %s, made from %s (%.7s)",
		team.Branch(tm.Name, "<role>"), from, base.Head)
	if tm.LeadRole != "" {
		text += "; " + tm.LeadRole + " works in the team's folder and merges"
	}
	if dirty, _ := git(tm.Cwd, "status", "--porcelain", "--untracked-files=no"); dirty != "" {
		text += ". Changes not committed in the team's folder aren't in the worktrees"
	}
	_, err := t.system(tm, text)
	return err
}

// placeMember gives a member of a worktrees team its worktree and records it
// as the member's folder. The lead stays in the team's folder, where what it
// merges lands on the branch the folder has.
func (t *Tracker) placeMember(tm store.Team, m *store.Member) error {
	if tm.Workspace != store.WorkspaceWorktrees || (tm.LeadRole != "" && m.Role == tm.LeadRole) {
		return nil
	}
	dir, err := t.memberWorktree(tm, m.Role)
	if err != nil {
		return err
	}
	m.Cwd = dir
	return t.Store.SetMemberCwd(tm.ID, m.Role, dir)
}

// memberWorktree makes the worktree a member works in, from the team folder's
// HEAD, or finds the one it had before: a member taken off the team and put
// back finds its work where it left it. It returns the member's folder: the
// worktree's copy of the team's folder.
func (t *Tracker) memberWorktree(tm store.Team, role string) (string, error) {
	prefix, err := git(tm.Cwd, "rev-parse", "--show-prefix")
	if err != nil {
		return "", err
	}
	path, branch := WorktreeDir(tm.Name, role), team.Branch(tm.Name, role)
	known, err := t.Store.Worktrees(tm.ID)
	if err != nil {
		return "", err
	}
	ours := slices.ContainsFunc(known, func(w store.Worktree) bool { return w.Role == role && w.Path == path })
	folder := func() string {
		// The team's folder may not be in the commit: then the worktree's top.
		if sub := filepath.Join(path, prefix); prefix != "" && isDir(sub) {
			return sub
		}
		return path
	}
	if _, err := os.Stat(path); err == nil {
		if ours {
			return folder(), nil
		}
		return "", fmt.Errorf("%s is there already; remove it, or pick another team name", path)
	}
	args := []string{"worktree", "add", "-b", branch, path, "HEAD"}
	if _, err := git(tm.Cwd, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		if !ours {
			return "", fmt.Errorf("the branch %s is there already; delete it, or pick another team name", branch)
		}
		args = []string{"worktree", "add", path, branch} // the branch outlived its worktree
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if _, err := git(tm.Cwd, args...); err != nil {
		return "", err
	}
	if err := t.Store.PutWorktree(store.Worktree{TeamID: tm.ID, Role: role, Path: path, Branch: branch,
		CreatedAt: t.World.Now()}); err != nil {
		return "", err
	}
	return folder(), nil
}

func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// dropWorktrees removes the worktrees and branches made for a team that isn't
// kept. No member's window opened, so nothing ran in them.
func (t *Tracker) dropWorktrees(tm store.Team) error {
	known, err := t.Store.Worktrees(tm.ID)
	if err != nil {
		return err
	}
	var errs []error
	for _, w := range known {
		if _, err := git(tm.Cwd, "worktree", "remove", "--force", w.Path); err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := git(tm.Cwd, "branch", "-D", w.Branch); err != nil {
			errs = append(errs, err)
		}
	}
	os.Remove(filepath.Dir(WorktreeDir(tm.Name, "role"))) // once empty
	return errors.Join(append(errs, t.Store.ForgetWorktrees(tm.ID))...)
}

// WorktreeState is a member's worktree, and how far its branch is from being
// merged.
type WorktreeState struct {
	store.Worktree
	Ahead   int    `json:"ahead"`             // commits on the branch that the base lacks
	Dirty   bool   `json:"dirty,omitempty"`   // changes not committed
	Removed bool   `json:"removed,omitempty"` // --clean took it, with its branch
	Note    string `json:"note,omitempty"`    // why it stays, or what went wrong
}

// State says, in a few words, where the worktree stands.
func (w WorktreeState) State() string {
	var parts []string
	switch {
	case w.Removed:
		parts = append(parts, "removed")
	case w.Ahead < 0:
	case w.Ahead == 0:
		parts = append(parts, "merged")
	default:
		parts = append(parts, plural(w.Ahead, "commit")+" to merge")
	}
	if w.Dirty {
		parts = append(parts, "changes not committed")
	}
	if w.Note != "" {
		parts = append(parts, w.Note)
	}
	return strings.Join(parts, "; ")
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what + "s"
}

// baseRef is what a team's branches are merged into: the branch its folder
// had when the team started, or, if its HEAD was detached then, the folder's
// HEAD now. It returns the ref, and its name for people.
func (t *Tracker) baseRef(tm store.Team) (string, string, error) {
	b, ok, err := t.Store.TeamBase(tm.ID)
	if err != nil {
		return "", "", err
	}
	if !ok || b.Branch == "" {
		return "HEAD", "the team folder's HEAD", nil
	}
	ref := "refs/heads/" + b.Branch
	if _, err := git(tm.Cwd, "rev-parse", "--verify", "--quiet", ref); err != nil {
		return "", "", fmt.Errorf("the branch %s, which the team started from, is gone; nothing can be called merged", b.Branch)
	}
	return ref, b.Branch, nil
}

// TeamWorktrees returns the worktrees a team still has, each with how many
// commits its branch has that the base lacks, and the base's name.
func (t *Tracker) TeamWorktrees(tm store.Team) ([]WorktreeState, string, error) {
	known, err := t.Store.Worktrees(tm.ID)
	if err != nil || len(known) == 0 {
		return nil, "", err
	}
	ref, name, err := t.baseRef(tm)
	if err != nil {
		return nil, "", err
	}
	out := make([]WorktreeState, len(known))
	for i, w := range known {
		out[i] = WorktreeState{Worktree: w, Ahead: -1}
		n, err := git(tm.Cwd, "rev-list", "--count", ref+".."+w.Branch)
		if err != nil {
			out[i].Note = err.Error()
			continue
		}
		out[i].Ahead, _ = strconv.Atoi(n)
		if !isDir(w.Path) {
			out[i].Note = "its folder is gone"
			continue
		}
		changes, err := git(w.Path, "status", "--porcelain")
		if err != nil {
			out[i].Note = err.Error()
		}
		out[i].Dirty = changes != ""
	}
	return out, name, nil
}

// cleanWorktrees removes the worktrees whose branches the base has every
// commit of, with their branches. A worktree stays when its member still
// runs, when its branch has commits the base lacks, or when it holds changes
// not committed; the board lists what stays.
func (t *Tracker) cleanWorktrees(tm store.Team, running []string) ([]WorktreeState, string, error) {
	states, base, err := t.TeamWorktrees(tm)
	if err != nil || len(states) == 0 {
		return states, base, err
	}
	var errs, removed, kept []string
	for i := range states {
		w := &states[i]
		switch {
		case w.Note != "" || w.Ahead != 0 || w.Dirty:
		case slices.Contains(running, w.Role):
			w.Note = "it still runs"
		default:
			if _, err := git(tm.Cwd, "worktree", "remove", w.Path); err != nil {
				w.Note = err.Error()
				break
			}
			w.Removed = true
			if _, err := git(tm.Cwd, "branch", "-D", w.Branch); err != nil {
				w.Note = "the branch stays: " + err.Error()
			}
			if err := t.Store.ForgetWorktree(tm.ID, w.Role); err != nil {
				errs = append(errs, err.Error())
			}
		}
		if w.Removed {
			removed = append(removed, w.Role)
		} else {
			kept = append(kept, w.Role+" ("+w.State()+")")
		}
	}
	os.Remove(filepath.Dir(WorktreeDir(tm.Name, "role"))) // once empty
	text := "cleaned up against " + base + ":"
	if len(removed) > 0 {
		text += " removed the worktrees and branches of " + strings.Join(removed, ", ") + ";"
	}
	if len(kept) > 0 {
		text += " kept " + strings.Join(kept, ", ") + ";"
	}
	if _, err := t.system(tm, strings.TrimSuffix(text, ";")); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return states, base, errors.New(strings.Join(errs, "; "))
	}
	return states, base, nil
}
