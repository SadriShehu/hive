package tui

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sadrishehu/hive/internal/paths"
)

// shownFolders is how many folder suggestions the new-agent form lists at once.
const shownFolders = 8

// folderSuggestions returns the folders that complete value: those in the
// folder it names up to its last slash whose names start with what follows,
// ignoring case. Hidden folders show once that starts with a dot. Each comes
// back as value reads with it filled in; ~ and relative paths stay as typed,
// and resolve as starting an agent resolves them.
func folderSuggestions(value string) []string {
	cut := strings.LastIndex(value, "/") + 1
	base, typed := value[:cut], strings.ToLower(value[cut:])
	dir := cmp.Or(paths.Expand(base), ".")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(strings.ToLower(name), typed) || (name[0] == '.' && typed == "") {
			continue
		}
		if !e.IsDir() {
			if e.Type()&os.ModeSymlink == 0 {
				continue
			}
			// a link to a folder is one too
			if fi, err := os.Stat(filepath.Join(dir, name)); err != nil || !fi.IsDir() {
				continue
			}
		}
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(strings.Compare(strings.ToLower(a), strings.ToLower(b)), strings.Compare(a, b))
	})
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = base + name
	}
	return out
}
