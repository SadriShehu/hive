// Package copilot connects GitHub Copilot CLI to hive through its hooks.
package copilot

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/paths"
)

const name = "copilot"

// Adapter is the Copilot CLI adapter. Its paths can be redirected for tests.
type Adapter struct {
	HomeDir         string
	HooksDir        string
	SessionStateDir string
	DBPath          string
}

// New returns the adapter for the user's Copilot CLI files.
func New() *Adapter { return &Adapter{} }

// Spec describes GitHub Copilot CLI.
func (a *Adapter) Spec() agent.Spec {
	return agent.Spec{
		Name:     name,
		New:      []string{"copilot", "--session-id", "{session}", "--interactive", "{prompt}"},
		Resume:   []string{"copilot", "--resume", "{id}", "--interactive", "{prompt}"},
		Process:  []string{"copilot"},
		Headless: []string{"-p", "--prompt"},

		TitleFlags:   []string{"-n", "--name"},
		SessionFlags: []string{"-r", "--resume", "--session-id"},
	}
}

func (a *Adapter) copilotHome() string {
	if a.HomeDir != "" {
		return a.HomeDir
	}
	if d := os.Getenv("COPILOT_HOME"); d != "" {
		return d
	}
	return filepath.Join(paths.Home(), ".copilot")
}

func (a *Adapter) hooksDir() string {
	if a.HooksDir != "" {
		return a.HooksDir
	}
	return filepath.Join(a.copilotHome(), "hooks")
}

func (a *Adapter) sessionStateDir() string {
	if a.SessionStateDir != "" {
		return a.SessionStateDir
	}
	return filepath.Join(a.copilotHome(), "session-state")
}

func (a *Adapter) dbPath() string {
	if a.DBPath != "" {
		return a.DBPath
	}
	return filepath.Join(a.copilotHome(), "session-store.db")
}

// ParseHook accepts the camelCase and VS Code-compatible Copilot hook payloads.
func (a *Adapter) ParseHook(stdin []byte, args []string, _ func(string) string) ([]agent.Event, error) {
	var in struct {
		SessionID       string          `json:"sessionId"`
		SessionIDSnake  string          `json:"session_id"`
		ParentID        string          `json:"parentId"`
		ParentIDSnake   string          `json:"parent_id"`
		HookEvent       string          `json:"hook_event_name"`
		Event           string          `json:"event"`
		Cwd             string          `json:"cwd"`
		Prompt          string          `json:"prompt"`
		InitialPrompt   string          `json:"initialPrompt"`
		InitialSnake    string          `json:"initial_prompt"`
		Transcript      string          `json:"transcriptPath"`
		TranscriptSnake string          `json:"transcript_path"`
		TranscriptJSON  string          `json:"transcript"`
		Title           string          `json:"title"`
		PID             int             `json:"pid"`
		Headless        bool            `json:"headless"`
		Internal        bool            `json:"internal"`
		Timestamp       json.RawMessage `json:"timestamp"`
		At              json.RawMessage `json:"at"`
	}
	if err := json.Unmarshal(stdin, &in); err != nil {
		return nil, errors.New("copilot hook payload: " + err.Error())
	}
	session := in.SessionID
	if session == "" {
		session = in.SessionIDSnake
	}
	if session == "" {
		return nil, errors.New("copilot hook payload has no sessionId")
	}
	event := in.Event
	if event == "" {
		event = in.HookEvent
	}
	if event == "" && len(args) > 0 {
		event = args[0]
	}
	parent := in.ParentID
	if parent == "" {
		parent = in.ParentIDSnake
	}
	if parent != "" && !strings.Contains(parent, ":") {
		parent = "copilot:" + parent
	}
	transcript := in.Transcript
	if transcript == "" {
		transcript = in.TranscriptSnake
	}
	if transcript == "" {
		transcript = in.TranscriptJSON
	}
	if transcript == "" {
		transcript = filepath.Join(a.sessionStateDir(), session, "events.jsonl")
	}
	ev := agent.Event{
		Tool: name, SessionID: session, At: hookTimestamp(in.Timestamp), PID: in.PID,
		ParentID: parent, Title: in.Title, Cwd: in.Cwd, Transcript: transcript,
		Internal: in.Internal, Headless: in.Headless,
	}
	if ev.At == 0 {
		ev.At = hookTimestamp(in.At)
	}
	switch event {
	case "start", "prompt", "busy", "working", "idle", "attention", "end", "update":
		ev.Type, _ = agent.ParseEventType(event)
		if event == "prompt" {
			ev.Prompt = in.Prompt
		}
	case "sessionStart", "SessionStart":
		ev.Type = agent.Start
		ev.Prompt = firstNonEmpty(in.InitialPrompt, in.InitialSnake)
	case "userPromptSubmitted", "UserPromptSubmit":
		ev.Type, ev.Prompt = agent.Prompt, in.Prompt
	case "preToolUse", "PreToolUse", "postToolUse", "PostToolUse", "subagentStart", "subagentStop":
		ev.Type = agent.Busy
	case "agentStop", "Stop":
		ev.Type = agent.Idle
	case "sessionEnd", "SessionEnd":
		ev.Type = agent.End
	default:
		ev.Type = agent.Update
	}
	return []agent.Event{ev}, nil
}

func hookTimestamp(raw json.RawMessage) int64 {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var ms int64
	if json.Unmarshal(raw, &ms) == nil {
		return ms
	}
	var stamp string
	if json.Unmarshal(raw, &stamp) == nil {
		return timestamp(stamp)
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// Installed reports whether the user-level hooks call hive on session start.
func (a *Adapter) Installed() bool {
	data, err := os.ReadFile(a.hooksPath())
	return err == nil && len(findOurs(data, "sessionStart")) > 0
}

// hooksPath is the dedicated user-level hook file hive edits.
func (a *Adapter) hooksPath() string {
	return filepath.Join(a.hooksDir(), "hive.json")
}

var ours = regexp.MustCompile(`hive'?\s+hook\s+copilot\s+(\w+)`)

// findOurs locates hive hook commands in one event's hook list.
func findOurs(data []byte, event string) []int {
	var refs []int
	gjson.GetBytes(data, "hooks."+event).ForEach(func(i, hook gjson.Result) bool {
		if m := ours.FindStringSubmatch(hook.Get("bash").String()); m != nil && m[1] == event {
			refs = append(refs, int(i.Int()))
		}
		return true
	})
	return refs
}
