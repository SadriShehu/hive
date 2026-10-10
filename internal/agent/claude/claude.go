// Package claude connects Claude Code to hive through its hooks.
package claude

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/proc"
	"github.com/sadrishehu/hive/internal/store"
)

const name = "claude"

// Adapter is the Claude Code adapter. The zero value uses the user's
// Claude Code files; the fields point it elsewhere.
type Adapter struct {
	SettingsPath string // settings.json that install edits
	ProjectsDir  string // where transcripts live
}

// New returns the adapter for the user's Claude Code configuration.
func New() *Adapter { return &Adapter{} }

// Spec describes Claude Code.
func (a *Adapter) Spec() agent.Spec {
	return agent.Spec{
		Name:      name,
		New:       []string{"claude", "--model", "{model}", "--session-id", "{session}", "{prompt}"},
		Resume:    []string{"claude", "--resume", "{id}", "{prompt}"},
		Process:   []string{"claude"},
		Headless:  []string{"-p", "--print"},
		ParentEnv: "CLAUDE_CODE_SESSION_ID",

		// The daemon and the hosts it runs background sessions in, and the
		// subcommands that manage things rather than run a session.
		HelperSubcommands: []string{
			"daemon", "bg-pty-host", "--bg-pty-host", "agents", "attach", "auth", "auto-mode",
			"doctor", "gateway", "import", "install", "logs", "mcp", "plugin", "plugins",
			"project", "respawn", "rm", "setup-token", "stop", "kill", "ultrareview",
			"update", "upgrade",
		},

		TitleFlags:   []string{"-n", "--name"},
		SessionFlags: []string{"-r", "--resume", "--session-id"},

		// The current generation; history adds what else an account ran.
		Models: []string{"claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-4-5"},
	}
}

// Prewarmed reports whether a session runs under Claude's daemon, which hosts
// each background session (`claude --bg`), and each spare it keeps started
// for the next one, in a bg-pty-host process.
func (a *Adapter) Prewarmed(chain []proc.Proc) bool {
	for _, p := range chain {
		if argv := p.Argv(); len(argv) > 1 && (argv[1] == "bg-pty-host" || argv[1] == "--bg-pty-host") {
			return true
		}
	}
	return false
}

// configDir is the user's Claude Code directory.
func configDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(paths.Home(), ".claude")
}

func (a *Adapter) settingsPath() string {
	if a.SettingsPath != "" {
		return a.SettingsPath
	}
	return filepath.Join(configDir(), "settings.json")
}

func (a *Adapter) projectsDir() string {
	if a.ProjectsDir != "" {
		return a.ProjectsDir
	}
	return filepath.Join(configDir(), "projects")
}

// hookInput is the JSON Claude Code pipes to every hook command.
type hookInput struct {
	SessionID           string `json:"session_id"`
	TranscriptPath      string `json:"transcript_path"`
	Cwd                 string `json:"cwd"`
	HookEventName       string `json:"hook_event_name"`
	Prompt              string `json:"prompt"`
	NotificationType    string `json:"notification_type"`
	Message             string `json:"message"`
	AgentID             string `json:"agent_id"`
	AgentType           string `json:"agent_type"`
	AgentTranscriptPath string `json:"agent_transcript_path"`
}

// ParseHook reads one Claude Code hook invocation.
func (a *Adapter) ParseHook(stdin []byte, _ []string, getenv func(string) string) ([]agent.Event, error) {
	var in hookInput
	if err := json.Unmarshal(stdin, &in); err != nil {
		return nil, errors.New("claude hook payload: " + err.Error())
	}
	if in.SessionID == "" {
		return nil, errors.New("claude hook payload has no session_id")
	}
	ev := agent.Event{Tool: name, SessionID: in.SessionID, Transcript: in.TranscriptPath}
	switch in.HookEventName {
	case "SessionStart":
		ev.Type = agent.Start
		ev.EnvFile = getenv("CLAUDE_ENV_FILE")
		// Later events report where the shell tool has cd'ed to; the folder
		// the session started in is its project, and resuming needs it.
		ev.Cwd = in.Cwd
	case "UserPromptSubmit":
		ev.Type = agent.Prompt
		ev.Prompt = in.Prompt
	case "PreToolUse", "PostToolUse", "PostToolUseFailure":
		ev.Type = agent.Busy
	case "Notification":
		ev.Type = notificationEvent(in)
	case "Stop", "StopFailure":
		ev.Type = agent.Idle
	case "SessionEnd":
		ev.Type = agent.End
	case "SubagentStart", "SubagentStop":
		if in.HookEventName == "SubagentStart" && in.AgentType == "" {
			return nil, nil // Claude's own helpers, with no task and no transcript
		}
		return subagentEvents(in), nil
	default:
		ev.Type = agent.Update
	}
	return []agent.Event{ev}, nil
}

func notificationEvent(in hookInput) agent.EventType {
	switch in.NotificationType {
	case "permission_prompt", "elicitation_dialog", "agent_needs_input":
		return agent.Attention
	case "idle_prompt":
		return agent.Idle
	case "":
		// Older versions send only a message.
		msg := strings.ToLower(in.Message)
		switch {
		case strings.Contains(msg, "permission"):
			return agent.Attention
		case strings.Contains(msg, "waiting for your input"):
			return agent.Idle
		}
	}
	return agent.Update
}

// subagentEvents records a Task subagent as an internal child of the session
// that started it. Its transcript sits next to the parent's:
// <project>/<session>/subagents/agent-<id>.jsonl.
func subagentEvents(in hookInput) []agent.Event {
	transcript := in.AgentTranscriptPath
	if transcript == "" && in.TranscriptPath != "" {
		transcript = filepath.Join(strings.TrimSuffix(in.TranscriptPath, ".jsonl"),
			"subagents", "agent-"+in.AgentID+".jsonl")
	}
	ev := agent.Event{
		Tool:       name,
		SessionID:  in.SessionID + "/" + in.AgentID,
		ParentID:   store.ID(name, in.SessionID),
		Internal:   true,
		Cwd:        in.Cwd,
		Transcript: transcript,
	}
	if in.HookEventName == "SubagentStop" {
		ev.Type = agent.End
		return []agent.Event{ev}
	}
	ev.Title = subagentTitle(transcript, in.AgentType)
	start, busy := ev, ev
	start.Type, busy.Type = agent.Start, agent.Busy
	return []agent.Event{start, busy}
}

// subagentTitle prefers the task description Claude stores beside the
// subagent's transcript.
func subagentTitle(transcript, agentType string) string {
	data, err := os.ReadFile(strings.TrimSuffix(transcript, ".jsonl") + ".meta.json")
	if err == nil {
		var meta struct {
			Description string `json:"description"`
		}
		if json.Unmarshal(data, &meta) == nil && meta.Description != "" {
			return meta.Description
		}
	}
	return agentType
}
