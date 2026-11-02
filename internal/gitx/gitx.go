// Package gitx runs the read-only git commands hive uses to look at a folder
// and a branch. Nothing here changes a repository: every command reads.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// timeout bounds each read-only command, so a locked repository never holds
// the tree or the board.
const timeout = 2 * time.Second

// Merge is what a dry merge of a branch into the base found.
type Merge struct {
	Result    string   `json:"result"` // one of the Result values below
	Conflicts []string `json:"conflicts,omitempty"`
}

// The results of MergeCheck.
const (
	Merged    = "merged"    // every commit is already in the base
	Clean     = "clean"     // it merges without a conflict
	Conflicts = "conflicts" // it does not
	Unknown   = "unknown"   // git is too old to tell
)

// Git runs git in dir and returns its output, trimmed. It is the one runner
// behind the read-only functions; the tracker's worktree setup shares it so
// there is a single way hive talks to git. Its error is what git said, without
// the hints.
func Git(dir string, args ...string) (string, error) {
	out, code, stderr, err := run(dir, 0, args...)
	if err != nil {
		return "", gitError(args, code, stderr, err)
	}
	return out, nil
}

// run runs git in dir, with a timeout when wait is positive, and reports its
// output, exit code and stderr.
func run(dir string, wait time.Duration, args ...string) (string, int, string, error) {
	ctx := context.Background()
	if wait > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, wait)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else {
			code = -1
		}
	}
	return strings.TrimSpace(out.String()), code, strings.TrimSpace(stderr.String()), err
}

// gitError words a failed command: what ran, and the first thing git said.
func gitError(args []string, code int, stderr string, err error) error {
	what := "git"
	if len(args) > 0 {
		what += " " + args[0]
	}
	if line := firstLine(stderr); line != "" {
		line = strings.TrimPrefix(strings.TrimPrefix(line, "fatal: "), "error: ")
		return fmt.Errorf("%s: %s", what, line)
	}
	if code > 0 {
		return fmt.Errorf("%s: exit status %d", what, code)
	}
	return fmt.Errorf("%s: %w", what, err)
}

func firstLine(s string) string {
	for line := range strings.SplitSeq(s, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "hint:") {
			return line
		}
	}
	return ""
}

// Head is the branch a folder has checked out, or its short hash when its
// HEAD is detached.
func Head(dir string) (string, error) {
	branch, code, stderr, err := run(dir, timeout, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err == nil && branch != "" {
		return branch, nil
	}
	// A detached HEAD is exit 1 with nothing to say, not a failure: name the
	// commit instead. Anything else (no repository) is an error.
	if err != nil && (code != 1 || firstLine(stderr) != "") {
		return "", gitError([]string{"symbolic-ref", "HEAD"}, code, stderr, err)
	}
	hash, code, stderr, err := run(dir, timeout, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", gitError([]string{"rev-parse", "HEAD"}, code, stderr, err)
	}
	return hash, nil
}

// AheadBehind counts the commits branch has that base lacks, and the ones base
// has that branch lacks.
func AheadBehind(dir, base, branch string) (ahead, behind int, err error) {
	out, code, stderr, err := run(dir, timeout, "rev-list", "--left-right", "--count", base+"..."+branch)
	if err != nil {
		return 0, 0, gitError([]string{"rev-list", base + "..." + branch}, code, stderr, err)
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("git rev-list %s...%s: %q", base, branch, out)
	}
	behind, _ = strconv.Atoi(fields[0])
	ahead, _ = strconv.Atoi(fields[1])
	return ahead, behind, nil
}

// DiffStat sums the changes base...branch makes: how many files, and how many
// lines added and removed. A binary file counts as one file with no lines.
func DiffStat(dir, base, branch string) (files, added, removed int, err error) {
	out, code, stderr, err := run(dir, timeout, "diff", "--numstat", base+"..."+branch)
	if err != nil {
		return 0, 0, 0, gitError([]string{"diff", "--numstat", base + "..." + branch}, code, stderr, err)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 2 {
			continue
		}
		files++
		if n, err := strconv.Atoi(fields[0]); err == nil {
			added += n
		}
		if n, err := strconv.Atoi(fields[1]); err == nil {
			removed += n
		}
	}
	return files, added, removed, nil
}

// Uncommitted counts the paths in dir with changes not committed, untracked
// ones included.
func Uncommitted(dir string) (int, error) {
	out, code, stderr, err := run(dir, timeout, "status", "--porcelain")
	if err != nil {
		return 0, gitError([]string{"status", "--porcelain"}, code, stderr, err)
	}
	n := 0
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n, nil
}

// MergeCheck asks git whether branch merges into base: merged when base
// already has every commit, clean when it would merge without a conflict, or
// conflicts with the files it can't settle. A git too old for --write-tree
// says unknown.
func MergeCheck(dir, base, branch string) (Merge, error) {
	_, code, _, err := run(dir, timeout, "merge-base", "--is-ancestor", branch, base)
	switch {
	case err == nil:
		return Merge{Result: Merged}, nil
	case code != 1:
		_, _, stderr, _ := run(dir, timeout, "merge-base", "--is-ancestor", branch, base)
		return Merge{}, gitError([]string{"merge-base", "--is-ancestor", branch, base}, code, stderr, err)
	}
	out, code, stderr, err := run(dir, timeout, "merge-tree", "--write-tree", "--name-only", base, branch)
	if err == nil {
		return Merge{Result: Clean}, nil
	}
	if oldGit(stderr) || code < 0 {
		return Merge{Result: Unknown}, nil
	}
	if code != 1 {
		return Merge{}, gitError([]string{"merge-tree", "--write-tree", "--name-only", base, branch}, code, stderr, err)
	}
	return Merge{Result: Conflicts, Conflicts: conflictFiles(out)}, nil
}

// oldGit reports whether git refused the merge-tree options because it is too
// old to have them.
func oldGit(stderr string) bool {
	for line := range strings.SplitSeq(stderr, "\n") {
		line = strings.ToLower(line)
		if strings.Contains(line, "unknown option") || strings.Contains(line, "usage: git merge-tree") {
			return true
		}
	}
	return false
}

// conflictFiles reads the names merge-tree --name-only lists after the tree
// it would write, up to the blank line before its messages.
func conflictFiles(out string) []string {
	lines := strings.Split(out, "\n")
	var names []string
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			break
		}
		names = append(names, strings.TrimSpace(line))
	}
	return names
}
