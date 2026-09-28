// Package agent defines what hive needs from each coding-agent tool. Linking,
// liveness and tmux control are shared; an adapter only describes its tool.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/sadrishehu/hive/internal/proc"
	"github.com/sadrishehu/hive/internal/store"
)

// EventType is a normalized lifecycle event.
type EventType string

const (
	Start     EventType = "start"     // session began or was reopened
	Prompt    EventType = "prompt"    // the user sent a prompt
	Busy      EventType = "busy"      // the agent is working
	Idle      EventType = "idle"      // the agent finished its turn
	Attention EventType = "attention" // the agent waits on the user: permission, a question
	End       EventType = "end"       // session is over
	Update    EventType = "update"    // details changed (title), no status change
)

// ParseEventType accepts an event name from the JSON hook contract.
func ParseEventType(s string) (EventType, bool) {
	switch t := EventType(s); t {
	case Start, Prompt, Busy, Idle, Attention, End, Update:
		return t, true
	case "working":
		return Busy, true
	}
	return "", false
}

// Event is one lifecycle report from an agent, as `hive hook` received it.
type Event struct {
	Tool      string
	SessionID string // the tool's own session ID
	Type      EventType
	At        int64 // epoch ms; 0 means now

	// PID is the agent process. 0 means hive finds it by walking up from the
	// hook process to the nearest process of this tool.
	PID int

	ParentID string // explicit parent as a hive session ID ("opencode:ses_…"), when the tool knows it
	Internal bool   // the tool's own subagent, running inside its parent's process
	Headless bool   // a non-interactive run the tool knows about

	Title      string
	Cwd        string
	Transcript string
	Prompt     string

	// EnvFile is a file whose exports reach the agent's shell tool (Claude's
	// CLAUDE_ENV_FILE). hive writes HIVE_PARENT there on Start so everything
	// the agent spawns knows its parent.
	EnvFile string
}

// Spec is the data part of an adapter, and all a config-defined tool has.
type Spec struct {
	Name string

	// New and Resume are argv templates. {prompt} and {session} expand to the
	// first prompt and a pre-assigned session ID, {id} to the session to resume.
	New    []string
	Resume []string

	// Process lists program names that identify the tool's processes.
	Process []string

	// Headless lists argv markers of a non-interactive run: a flag ("-p")
	// anywhere, or a subcommand ("run") right after the program name.
	Headless []string

	// ParentEnv is a variable the tool sets to its own session ID for the
	// commands it runs, if any (CLAUDE_CODE_SESSION_ID).
	ParentEnv string

	// TitleFlags and SessionFlags name the flags that title a new session or
	// pick the session to continue ("--title", "-s"), so commands found in
	// history can be matched to the sessions they started.
	TitleFlags   []string
	SessionFlags []string
}

// Matches reports whether p is one of the tool's processes.
func (s Spec) Matches(p proc.Proc) bool { return p.Runs(s.Process) }

// IsHeadless reports whether argv is a non-interactive run of the tool.
func (s Spec) IsHeadless(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	for _, m := range s.Headless {
		if strings.HasPrefix(m, "-") {
			if slices.Contains(argv[1:], m) {
				return true
			}
		} else if argv[1] == m {
			return true
		}
	}
	return false
}

// Adapter connects one tool to hive.
type Adapter interface {
	Spec() Spec

	// ParseHook turns one `hive hook <tool>` invocation into events. stdin is
	// the piped payload (possibly empty), args the extra arguments.
	ParseHook(stdin []byte, args []string, getenv func(string) string) ([]Event, error)

	// Install wires the tool to call `hiveBin hook <tool>`, returning a
	// one-line summary. It is safe to run repeatedly.
	Install(hiveBin string) (string, error)

	// Uninstall removes exactly what Install added.
	Uninstall() (string, error)

	// Installed reports whether the integration is in place.
	Installed() bool
}

// ShellCommand is a shell command a session ran, found in its history.
type ShellCommand struct {
	SessionID string // hive ID of the session that ran it
	At        int64  // epoch ms
	Cwd       string // where it ran, when known
	Command   string
}

// Importer reads a tool's own storage, so sessions from before hive was
// installed appear, with details hooks don't carry, like titles.
type Importer interface {
	// Import upserts the tool's sessions into st, passes each shell command
	// they ran to found, and returns how many sessions it read. It skips what
	// hasn't changed since the last import.
	Import(ctx context.Context, st *store.Store, found func(ShellCommand)) (int, error)
}

// jsonEvent is the hook contract any tool can speak: one JSON object per
// event on stdin of `hive hook <tool>`.
type jsonEvent struct {
	Event      string `json:"event"`
	SessionID  string `json:"session_id"`
	ParentID   string `json:"parent_id"`
	PID        int    `json:"pid"`
	At         int64  `json:"at"`
	Title      string `json:"title"`
	Cwd        string `json:"cwd"`
	Prompt     string `json:"prompt"`
	Transcript string `json:"transcript"`
	Internal   bool   `json:"internal"`
	Headless   bool   `json:"headless"`
}

// ParseJSON reads events in the generic hook contract.
func ParseJSON(tool string, data []byte) ([]Event, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var out []Event
	for {
		var j jsonEvent
		err := dec.Decode(&j)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s hook payload: %w", tool, err)
		}
		t, ok := ParseEventType(j.Event)
		if !ok {
			return nil, fmt.Errorf("%s hook payload: unknown event %q", tool, j.Event)
		}
		if j.SessionID == "" {
			return nil, fmt.Errorf("%s hook payload: missing session_id", tool)
		}
		out = append(out, Event{
			Tool: tool, SessionID: j.SessionID, Type: t, At: j.At, PID: j.PID,
			ParentID: j.ParentID, Internal: j.Internal, Headless: j.Headless,
			Title: j.Title, Cwd: j.Cwd, Prompt: j.Prompt, Transcript: j.Transcript,
		})
	}
	return out, nil
}
