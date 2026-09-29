package codex

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

const tailBytes = 512 << 10

// Tail returns the last n lines of a session's transcript.
func (a *Adapter) Tail(s store.Session, n int) ([]agent.Line, error) {
	if n <= 0 {
		return nil, nil
	}
	path := firstNonEmpty(s.Transcript, a.rolloutPath(s.NativeID))
	if path == "" {
		return nil, errors.New("no transcript recorded for this session")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := max(0, fi.Size()-tailBytes)
	data := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(data, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if start > 0 {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	var lines []agent.Line
	for raw := range bytes.SplitSeq(data, []byte("\n")) {
		rec, ok := parseLine(raw)
		if !ok || !rec.isItem() {
			continue
		}
		lines = append(lines, itemLines(rec.payload)...)
	}
	return lines[max(0, len(lines)-n):], nil
}

func itemLines(payload gjson.Result) []agent.Line {
	switch payload.Get("type").String() {
	case "message":
		text := messageText(payload.Get("content"))
		if text == "" {
			return nil
		}
		switch payload.Get("role").String() {
		case "user":
			return []agent.Line{{Role: "user", Text: text}}
		case "assistant":
			return []agent.Line{{Role: "assistant", Text: text}}
		}
	case "function_call":
		return []agent.Line{{Role: "tool", Text: callSummary(payload.Get("name").String(), payload.Get("arguments"))}}
	case "local_shell_call":
		command := commandString(payload.Get("action.command"))
		return []agent.Line{{Role: "tool", Text: "shell: " + firstLine(command)}}
	case "custom_tool_call":
		return []agent.Line{{Role: "tool", Text: customCallSummary(payload.Get("name").String(), payload.Get("input").String())}}
	}
	return nil
}

func callSummary(name string, arguments gjson.Result) string {
	if command, _ := callCommand(arguments); command != "" {
		return name + ": " + firstLine(command)
	}
	if arguments.Type == gjson.String {
		arguments = gjson.Parse(arguments.String())
	}
	for _, key := range []string{"description", "path", "file_path", "pattern", "query", "url", "prompt"} {
		if v := arguments.Get(key).String(); v != "" {
			return name + ": " + firstLine(v)
		}
	}
	return name
}

func customCallSummary(name, input string) string {
	if commands := codeModeCommands(input); len(commands) > 0 {
		return name + ": " + firstLine(commands[0])
	}
	if input = strings.TrimSpace(input); input != "" {
		return name + ": " + firstLine(input)
	}
	return name
}

func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}

// CheckResume refuses sessions whose Codex session file is gone.
func (a *Adapter) CheckResume(s store.Session) error {
	if s.NativeID == "" {
		return errors.New("Codex session has no native session ID")
	}
	path := firstNonEmpty(s.Transcript, a.rolloutPath(s.NativeID))
	if path == "" {
		return errors.New("Codex session file was removed; it can't be resumed")
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("Codex session file was removed; it can't be resumed")
		}
		return err
	}
	return nil
}
