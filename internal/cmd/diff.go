package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/gitx"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
)

func newDiffCmd() *cobra.Command {
	var asJSON, stat bool
	cmd := &cobra.Command{
		Use:   "diff <id>",
		Short: "Show what a session or a worktree member changed",
		Long: "Show what a session changed. For a member of a worktrees team, that is the commits\n" +
			"on its branch since the team's base, then what is left uncommitted in its worktree;\n" +
			"for any other session, the uncommitted changes in its folder. The diff goes through\n" +
			"git, so it uses your pager and diff tool. --stat prints the summary and file list,\n" +
			"and --json prints them as JSON.",
		Args: oneArg("session", "diff <id>", "the session's folder, or a worktree member's branch"),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, tr, sessions, err := openTracker()
			if err != nil {
				return err
			}
			defer st.Close()
			s, err := find(cmd.Context(), tr, sessions, args[0])
			if err != nil {
				return err
			}
			teamID, role := memberOfSession(st, s.ID)
			rep, err := tr.Changes(s.Cwd, teamID, role)
			if err != nil {
				return err
			}
			if rep.NotRepo {
				return fmt.Errorf("not a git repository: %s", homePath(rep.Dir))
			}
			label := changeLabel(rep, sharedSessions(sessions, s), role, title(s))
			if asJSON {
				files := rep.Files
				if files == nil {
					files = []gitx.FileChange{}
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(struct {
					Label       string            `json:"label"`
					Base        string            `json:"base,omitempty"`
					Branch      string            `json:"branch,omitempty"`
					Files       []gitx.FileChange `json:"files"`
					Uncommitted int               `json:"uncommitted"`
					Ahead       int               `json:"ahead"`
				}{label, rep.Base, rep.Branch, files, rep.Uncommitted, rep.Ahead})
			}
			if stat {
				printChangeStat(rep, label)
				return nil
			}
			return streamChanges(rep)
		},
	}
	cmd.Flags().BoolVar(&stat, "stat", false, "print the summary and the file list, not the diff")
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON: the label, base, branch, files, uncommitted and ahead")
	return cmd
}

// memberOfSession finds the team and role a session plays, if it is a member.
func memberOfSession(st *store.Store, id string) (teamID, role string) {
	members, err := st.AllMembers()
	if err != nil {
		return "", ""
	}
	for _, m := range members {
		if m.Active() && m.SessionID == id {
			return m.TeamID, m.Role
		}
	}
	return "", ""
}

// sharedSessions counts the other live sessions in a session's folder, so a
// plain folder's changes aren't mistaken for one session's own.
func sharedSessions(sessions []store.Session, s store.Session) int {
	if s.Cwd == "" {
		return 0
	}
	n := 0
	for _, o := range sessions {
		if o.ID != s.ID && o.Live() && o.Kind != store.KindInternal && o.Cwd == s.Cwd {
			n++
		}
	}
	return n
}

// changeLabel words what the diff covers.
func changeLabel(rep tracker.ChangeReport, shared int, role, who string) string {
	if rep.Worktree {
		return "changes on " + rep.Branch + " vs " + rep.Base
	}
	label := "changes in this folder"
	if shared > 0 {
		if role != "" {
			who = role
		}
		label += fmt.Sprintf(" · shared with %d live sessions, so not only %s's", shared, who)
	}
	return label
}

// printChangeStat prints the label, the summary and the files, as the TUI
// draws them.
func printChangeStat(rep tracker.ChangeReport, label string) {
	fmt.Println(label)
	fmt.Println(changeSummary(rep))
	largest := 0
	for _, f := range rep.Files {
		largest = max(largest, f.Added+f.Removed)
	}
	width := boardWidth()
	for _, f := range rep.Files {
		status := f.Status
		if status == "" {
			status = "M"
		}
		counts := fmt.Sprintf("+%d −%d", f.Added, f.Removed)
		pathW := max(8, min(48, width-2-1-12-1-9-1))
		path := pad(truncate(f.Path, pathW), pathW)
		line := fmt.Sprintf("  %s %s %s %s", status, path, pad(counts, 12), changeBarText(f, largest))
		fmt.Println(truncate(line, width))
	}
}

// changeSummary counts the files, lines, uncommitted paths and commits.
func changeSummary(rep tracker.ChangeReport) string {
	added, removed := 0, 0
	for _, f := range rep.Files {
		added, removed = added+f.Added, removed+f.Removed
	}
	if len(rep.Files) == 0 && rep.Uncommitted == 0 && rep.Ahead == 0 {
		return "no changes"
	}
	parts := []string{fileCount(len(rep.Files))}
	if added+removed > 0 {
		parts = append(parts, fmt.Sprintf("+%d −%d", added, removed))
	}
	if rep.Uncommitted > 0 {
		parts = append(parts, fmt.Sprintf("%d uncommitted", rep.Uncommitted))
	}
	if rep.Ahead > 0 {
		parts = append(parts, commitCount(rep.Ahead))
	}
	return strings.Join(parts, " · ")
}

func fileCount(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

func commitCount(n int) string {
	if n == 1 {
		return "1 commit ahead"
	}
	return fmt.Sprintf("%d commits ahead", n)
}

// changeBarText is the nine-cell bar the TUI draws, without colour.
func changeBarText(f gitx.FileChange, largest int) string {
	n := 0
	if largest > 0 {
		n = int(float64(f.Added+f.Removed)*9/float64(largest) + 0.5)
	}
	if n == 0 && f.Added+f.Removed > 0 {
		n = 1
	}
	n = min(9, max(0, n))
	return strings.Repeat("▓", n) + strings.Repeat("░", 9-n)
}

// streamChanges runs git with stdout and stderr passed straight through, so
// the diff goes to the user's pager and diff tool. A worktree member's
// committed range comes first, then whatever is left uncommitted.
func streamChanges(rep tracker.ChangeReport) error {
	if rep.Worktree {
		if err := streamGit(rep.Repo, "diff", rep.BaseRef+"..."+rep.Branch); err != nil {
			return err
		}
	}
	if !hasHead(rep.Dir) {
		return nil // nothing committed yet; every path is untracked
	}
	return streamGit(rep.Dir, "diff", "HEAD")
}

func streamGit(dir string, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", args[0], err)
	}
	return nil
}

func hasHead(dir string) bool {
	return exec.Command("git", "-C", dir, "rev-parse", "--verify", "--quiet", "HEAD").Run() == nil
}
