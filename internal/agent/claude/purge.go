package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

// Purge deletes a session the way Claude Code keeps it. A background session
// (`claude --bg`) is removed through `claude rm` first, which refuses while
// its worktree has work that isn't pushed; then go its transcript and the
// folders Claude keeps per session. A subagent's files sit in its parent's
// folder, and only they go.
func (a *Adapter) Purge(ctx context.Context, s store.Session) error {
	session, subagent, isSub := strings.Cut(s.NativeID, "/")
	if !agent.SafeID(session) || isSub && !agent.SafeID(subagent) {
		return fmt.Errorf("unexpected session ID %q", s.NativeID)
	}
	var doomed []string
	if isSub {
		for _, project := range a.projects() {
			dir := filepath.Join(project, session, "subagents")
			doomed = append(doomed, filepath.Join(dir, "agent-"+subagent+".jsonl"),
				filepath.Join(dir, "agent-"+subagent+".meta.json"))
		}
		return removeAll(doomed)
	}
	if err := removeJob(ctx, session); err != nil {
		return err
	}
	for _, project := range a.projects() {
		doomed = append(doomed, filepath.Join(project, session+".jsonl"), filepath.Join(project, session))
	}
	for _, dir := range []string{"file-history", "session-env", "tasks"} {
		doomed = append(doomed, filepath.Join(configDir(), dir, session))
	}
	return removeAll(doomed)
}

// projects returns the folders Claude keeps transcripts in, one per project.
func (a *Adapter) projects() []string {
	entries, _ := os.ReadDir(a.projectsDir())
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(a.projectsDir(), e.Name()))
		}
	}
	return out
}

// removeJob removes a background session's record, if session is one: the
// daemon keeps it under jobs/, named by the ID's first 8 characters.
func removeJob(ctx context.Context, session string) error {
	short := session[:min(8, len(session))]
	data, err := os.ReadFile(filepath.Join(configDir(), "jobs", short, "state.json"))
	if err != nil {
		return nil
	}
	var job struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(data, &job) != nil || job.SessionID != session {
		return nil
	}
	_, err = agent.RunTool(ctx, nil, "claude", "rm", short)
	return err
}

func removeAll(paths []string) error {
	var errs []error
	for _, p := range paths {
		if err := os.RemoveAll(p); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
