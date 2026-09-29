// Package codex connects the OpenAI Codex CLI to hive through its hooks.
package codex

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
)

const name = "codex"

// Adapter is the Codex adapter. Its paths can be redirected for tests.
type Adapter struct {
	HomeDir     string
	HooksPath   string
	SessionsDir string
	StateDBPath string
}

// New returns the adapter for the user's Codex files.
func New() *Adapter { return &Adapter{} }

// Spec describes the Codex CLI.
func (a *Adapter) Spec() agent.Spec {
	return agent.Spec{
		Name:    name,
		New:     []string{"codex", "{prompt}"},
		Resume:  []string{"codex", "resume", "{id}", "{prompt}"},
		Process: []string{"codex"},
		HelperSubcommands: []string{
			"agents", "login", "logout", "mcp", "mcp-server", "plugin", "app-server", "remote-control", "app",
			"completion", "update", "doctor", "sandbox", "debug", "apply", "a", "queue", "archive", "delete",
			"migrate-rollouts", "unarchive", "cloud", "exec-server", "features", "help",
		},
		Headless:     []string{"exec", "e", "review"},
		ParentEnv:    "CODEX_THREAD_ID",
		SessionFlags: []string{"resume"},
	}
}

func (a *Adapter) codexHome() string {
	if a.HomeDir != "" {
		return a.HomeDir
	}
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	return filepath.Join(paths.Home(), ".codex")
}

func (a *Adapter) hooksPath() string {
	if a.HooksPath != "" {
		return a.HooksPath
	}
	return filepath.Join(a.codexHome(), "hooks.json")
}

func (a *Adapter) sessionsDir() string {
	if a.SessionsDir != "" {
		return a.SessionsDir
	}
	return filepath.Join(a.codexHome(), "sessions")
}

func (a *Adapter) archivedSessionsDir() string {
	return filepath.Join(filepath.Dir(a.sessionsDir()), "archived_sessions")
}

var stateDBName = regexp.MustCompile(`^state_(\d+)\.sqlite$`)

func (a *Adapter) stateDBPath() string {
	if a.StateDBPath != "" {
		return a.StateDBPath
	}
	matches, _ := filepath.Glob(filepath.Join(a.codexHome(), "state_*.sqlite"))
	sort.Slice(matches, func(i, j int) bool { return stateDBVersion(matches[i]) > stateDBVersion(matches[j]) })
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func stateDBVersion(path string) int {
	m := stateDBName.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return -1
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func (a *Adapter) rolloutGlobs() []string {
	return []string{
		filepath.Join(a.sessionsDir(), "*", "*", "*", "rollout-*.jsonl"),
		filepath.Join(a.archivedSessionsDir(), "rollout-*.jsonl"),
	}
}

func (a *Adapter) rolloutPath(id string) string {
	if id == "" {
		return ""
	}
	for _, pattern := range a.rolloutGlobs() {
		matches, _ := filepath.Glob(filepath.Join(filepath.Dir(pattern), "rollout-*-"+id+".jsonl"))
		if len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

type hookInput struct {
	Event               string `json:"event"`
	SessionID           string `json:"session_id"`
	TranscriptPath      string `json:"transcript_path"`
	Cwd                 string `json:"cwd"`
	HookEventName       string `json:"hook_event_name"`
	Prompt              string `json:"prompt"`
	AgentID             string `json:"agent_id"`
	AgentType           string `json:"agent_type"`
	AgentTranscriptPath string `json:"agent_transcript_path"`
}

// ParseHook maps Codex hook payloads onto hive events. SubagentStart and
// SubagentStop become events for the subagent itself.
func (a *Adapter) ParseHook(stdin []byte, _ []string, _ func(string) string) ([]agent.Event, error) {
	var in hookInput
	if err := json.Unmarshal(stdin, &in); err != nil {
		return nil, errors.New("codex hook payload: " + err.Error())
	}
	if in.HookEventName == "" && in.Event != "" {
		return agent.ParseJSON(name, stdin)
	}
	if in.SessionID == "" {
		return nil, errors.New("codex hook payload has no session_id")
	}
	ev := agent.Event{Tool: name, SessionID: in.SessionID, Transcript: in.TranscriptPath}
	switch in.HookEventName {
	case "SessionStart":
		ev.Type = agent.Start
		ev.Cwd = in.Cwd
	case "UserPromptSubmit":
		ev.Type = agent.Prompt
		ev.Prompt = in.Prompt
	case "PreToolUse", "PostToolUse", "PreCompact", "PostCompact":
		ev.Type = agent.Busy
	case "PermissionRequest":
		ev.Type = agent.Attention
	case "Stop", "Interrupt":
		ev.Type = agent.Idle
	case "SessionEnd":
		ev.Type = agent.End
	case "SubagentStart", "SubagentStop":
		return subagentEvents(in), nil
	default:
		ev.Type = agent.Update
	}
	return []agent.Event{ev}, nil
}

func subagentEvents(in hookInput) []agent.Event {
	if in.AgentID == "" {
		return nil
	}
	ev := agent.Event{
		Tool:       name,
		SessionID:  in.AgentID,
		ParentID:   store.ID(name, in.SessionID),
		Internal:   true,
		Title:      in.AgentType,
		Cwd:        in.Cwd,
		Transcript: in.AgentTranscriptPath,
	}
	if in.HookEventName == "SubagentStop" {
		ev.Type = agent.End
		return []agent.Event{ev}
	}
	start, busy := ev, ev
	start.Type, busy.Type = agent.Start, agent.Busy
	return []agent.Event{start, busy}
}
