package codex

import (
	"context"
	"fmt"
	"regexp"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

var threadID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Purge deletes a thread through Codex itself, `codex delete`, which removes
// its rollout and its row in the state database. A spawned agent is a thread
// of its own and goes the same way.
func (a *Adapter) Purge(ctx context.Context, s store.Session) error {
	if !threadID.MatchString(s.NativeID) {
		return fmt.Errorf("unexpected thread ID %q", s.NativeID)
	}
	_, err := agent.RunTool(ctx, []string{"CODEX_HOME=" + a.codexHome()}, "codex", "delete", "--force", s.NativeID)
	if err != nil && a.rolloutPath(s.NativeID) == "" {
		return nil // Codex says it failed when the thread is gone already
	}
	return err
}
