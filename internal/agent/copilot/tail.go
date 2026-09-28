package copilot

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

const tailBytes = 512 << 10

// Tail returns the end of the Copilot session's event log.
func (a *Adapter) Tail(s store.Session, n int) ([]agent.Line, error) {
	if n <= 0 {
		return nil, nil
	}
	path := s.Transcript
	if path == "" {
		path = filepath.Join(a.sessionStateDir(), s.NativeID, "events.jsonl")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := max(int64(0), info.Size()-tailBytes)
	data := make([]byte, info.Size()-start)
	if _, err := f.ReadAt(data, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if start > 0 {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	var lines []agent.Line
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64<<10), 64<<20)
	for scanner.Scan() {
		raw := scanner.Bytes()
		switch gjson.GetBytes(raw, "type").String() {
		case "user.message":
			if text := strings.TrimSpace(gjson.GetBytes(raw, "data.content").String()); text != "" {
				lines = append(lines, agent.Line{Role: "user", Text: text})
			}
		case "assistant.message":
			if text := strings.TrimSpace(gjson.GetBytes(raw, "data.content").String()); text != "" {
				lines = append(lines, agent.Line{Role: "assistant", Text: text})
			}
		case "tool.execution_start":
			name := gjson.GetBytes(raw, "data.toolName").String()
			input := gjson.GetBytes(raw, "data.arguments")
			lines = append(lines, agent.Line{Role: "tool", Text: toolSummary(name, input)})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read Copilot event log: %w", err)
	}
	return lines[max(0, len(lines)-n):], nil
}

func toolSummary(name string, input gjson.Result) string {
	for _, key := range []string{"command", "intent", "description", "path", "query", "url"} {
		if value := input.Get(key).String(); value != "" {
			value, _, _ = strings.Cut(value, "\n")
			if name != "" {
				return name + ": " + value
			}
			return value
		}
	}
	return name
}

// CheckResume refuses sessions whose local Copilot state has been removed.
func (a *Adapter) CheckResume(s store.Session) error {
	if s.NativeID == "" {
		return errors.New("Copilot session has no native session ID")
	}
	if _, err := os.Stat(filepath.Join(a.sessionStateDir(), s.NativeID)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("Copilot session state was removed; it can't be resumed")
		}
		return err
	}
	return nil
}
