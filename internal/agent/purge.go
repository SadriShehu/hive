package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/sadrishehu/hive/internal/store"
)

// Purger is implemented by adapters that can delete a session from the tool's
// own storage for good: its transcript and whatever else the tool keeps for
// it, the way the tool itself would. A session already gone is not an error.
type Purger interface {
	Purge(ctx context.Context, s store.Session) error
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// SafeID reports whether id can name a file or folder: nothing in it can
// reach outside the folder it is joined to, or match more than itself in a
// glob.
func SafeID(id string) bool { return safeID.MatchString(id) }

// RunTool runs one of a tool's own commands with no terminal, adding env to
// hive's environment. The error carries the last line the command printed.
func RunTool(ctx context.Context, env []string, argv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), env...)
	raw, err := cmd.CombinedOutput()
	out := strings.TrimSpace(ansi.Strip(string(raw)))
	if err != nil {
		name := strings.Join(argv[:min(3, len(argv))], " ")
		if out == "" {
			return out, fmt.Errorf("%s: %w", name, err)
		}
		lines := strings.Split(out, "\n")
		return out, fmt.Errorf("%s: %s", name, strings.TrimSpace(lines[len(lines)-1]))
	}
	return out, nil
}
