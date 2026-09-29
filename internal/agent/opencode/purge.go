package opencode

import (
	"context"
	"fmt"
	"strings"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

// Purge deletes a session through opencode itself, `opencode session delete`,
// which takes its messages with it.
func (a *Adapter) Purge(ctx context.Context, s store.Session) error {
	if !agent.SafeID(s.NativeID) {
		return fmt.Errorf("unexpected session ID %q", s.NativeID)
	}
	out, err := agent.RunTool(ctx, nil, "opencode", "session", "delete", s.NativeID)
	if err != nil && strings.Contains(out, "Session not found") {
		return nil // gone already, perhaps with its parent
	}
	return err
}
