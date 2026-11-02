package gitx

import (
	"fmt"
	"strconv"
	"strings"
)

// FileChange is one path a change set touches.
type FileChange struct {
	Path    string `json:"path"`
	Status  string `json:"status"` // A, M, D, R, C, or ? for untracked
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// IsRepo reports whether dir is in a git repository.
func IsRepo(dir string) bool {
	_, _, _, err := run(dir, timeout, "rev-parse", "--git-dir")
	return err == nil
}

// Committed returns the files branch changes since base, and the unified diff
// of base...branch.
func Committed(dir, base, branch string) ([]FileChange, string, error) {
	rng := base + "..." + branch
	files, err := namedFiles(dir, rng)
	if err != nil {
		return nil, "", err
	}
	diff, code, stderr, err := run(dir, timeout, "diff", rng)
	if err != nil {
		return nil, "", gitError([]string{"diff", rng}, code, stderr, err)
	}
	return files, diff, nil
}

// UncommittedChanges returns the paths a folder has not committed, untracked
// ones by name, and the unified diff of the tracked changes since HEAD.
func UncommittedChanges(dir string) ([]FileChange, string, error) {
	status, code, stderr, err := runRaw(dir, timeout, "status", "--porcelain")
	if err != nil {
		return nil, "", gitError([]string{"status", "--porcelain"}, code, stderr, err)
	}
	files := statusFiles(status)
	counts, _ := numstat(dir, "HEAD") // no HEAD yet: the paths are still listed
	for i := range files {
		if n, ok := counts[files[i].Path]; ok {
			files[i].Added, files[i].Removed = n.Added, n.Removed
		}
	}
	diff, _, _, err := run(dir, timeout, "diff", "HEAD") // no HEAD yet: nothing tracked
	if err != nil {
		diff = ""
	}
	return files, diff, nil
}

// AheadOfUpstream counts the commits HEAD has that its upstream lacks, or 0
// when the branch has no upstream.
func AheadOfUpstream(dir string) (int, error) {
	if _, code, _, err := run(dir, timeout, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err != nil {
		if code == 128 || code == 1 {
			return 0, nil // no upstream configured
		}
		return 0, fmt.Errorf("git rev-parse @{u}: %w", err)
	}
	out, code, stderr, err := run(dir, timeout, "rev-list", "--count", "@{u}..HEAD")
	if err != nil {
		return 0, gitError([]string{"rev-list", "@{u}..HEAD"}, code, stderr, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("git rev-list @{u}..HEAD: %q", out)
	}
	return n, nil
}

// namedFiles lists the files a diff range touches, with their status and the
// lines each adds and removes.
func namedFiles(dir, rng string) ([]FileChange, error) {
	out, code, stderr, err := run(dir, timeout, "diff", "--name-status", rng)
	if err != nil {
		return nil, gitError([]string{"diff", "--name-status", rng}, code, stderr, err)
	}
	files := nameStatusFiles(out)
	counts, err := numstat(dir, rng)
	if err != nil {
		return nil, err
	}
	for i := range files {
		if n, ok := counts[files[i].Path]; ok {
			files[i].Added, files[i].Removed = n.Added, n.Removed
		}
	}
	return files, nil
}

// numstat sums the lines each path adds and removes in a diff range; a binary
// file is listed with no lines.
func numstat(dir, rng string) (map[string]FileChange, error) {
	out, code, stderr, err := run(dir, timeout, "diff", "--numstat", rng)
	if err != nil {
		return nil, gitError([]string{"diff", "--numstat", rng}, code, stderr, err)
	}
	counts := map[string]FileChange{}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		var c FileChange
		c.Added, _ = strconv.Atoi(fields[0]) // "-" for binary stays 0
		c.Removed, _ = strconv.Atoi(fields[1])
		c.Path = fields[2]
		counts[c.Path] = c
	}
	return counts, nil
}

// nameStatusFiles reads `git diff --name-status`: a status letter, then the
// path, or the old and new paths for a rename or copy.
func nameStatusFiles(out string) []FileChange {
	var files []FileChange
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || fields[0] == "" {
			continue
		}
		f := FileChange{Status: string(fields[0][0]), Path: fields[1]}
		if (f.Status == "R" || f.Status == "C") && len(fields) > 2 {
			f.Path = fields[2]
		}
		files = append(files, f)
	}
	return files
}

// statusFiles reads `git status --porcelain`: two status letters, a space,
// then the path (or "old -> new").
func statusFiles(out string) []FileChange {
	var files []FileChange
	for line := range strings.SplitSeq(out, "\n") {
		if len(line) < 4 {
			continue
		}
		status := line[:2]
		letter := "M"
		switch {
		case status == "??":
			letter = "?"
		case status[0] != ' ':
			letter = string(status[0])
		case status[1] != ' ':
			letter = string(status[1])
		}
		path := strings.TrimSpace(line[3:])
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		files = append(files, FileChange{Path: path, Status: letter})
	}
	return files
}
