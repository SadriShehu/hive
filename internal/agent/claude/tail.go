package claude

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

// tailBytes is how much of a transcript's end the preview reads.
const tailBytes = 512 << 10

// Tail returns the last n entries of a session's transcript.
func (a *Adapter) Tail(s store.Session, n int) ([]agent.Line, error) {
	if s.Transcript == "" {
		return nil, errors.New("no transcript recorded for this session")
	}
	f, err := os.Open(s.Transcript)
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
			data = data[i+1:] // the first line is cut
		}
	}
	var lines []agent.Line
	for raw := range bytes.SplitSeq(data, []byte("\n")) {
		switch gjson.GetBytes(raw, "type").String() {
		case "user":
			if text := userText(raw); text != "" {
				lines = append(lines, agent.Line{Role: "user", Text: text})
			}
		case "assistant":
			gjson.GetBytes(raw, "message.content").ForEach(func(_, c gjson.Result) bool {
				switch c.Get("type").String() {
				case "text":
					if text := strings.TrimSpace(c.Get("text").String()); text != "" {
						lines = append(lines, agent.Line{Role: "assistant", Text: text})
					}
				case "tool_use":
					lines = append(lines, agent.Line{Role: "tool", Text: toolSummary(c.Get("name").String(), c.Get("input"))})
				}
				return true
			})
		}
	}
	return lines[max(0, len(lines)-n):], nil
}

// toolSummary is a one-line description of a tool call.
func toolSummary(name string, input gjson.Result) string {
	for _, key := range []string{"description", "command", "file_path", "pattern", "url", "query", "prompt", "skill"} {
		if v := input.Get(key).String(); v != "" {
			v, _, _ = strings.Cut(v, "\n")
			return name + ": " + v
		}
	}
	return name
}
