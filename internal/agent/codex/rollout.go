package codex

import (
	"bytes"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/sadrishehu/hive/internal/agent"
)

type record struct {
	kind    string
	payload gjson.Result
	at      int64
}

var itemKinds = []string{"message", "function_call", "local_shell_call", "custom_tool_call", "reasoning", "function_call_output", "custom_tool_call_output"}

func parseLine(raw []byte) (record, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || !gjson.ValidBytes(raw) {
		return record{}, false
	}
	line := gjson.ParseBytes(raw)
	rec := record{kind: line.Get("type").String(), at: timestamp(line.Get("timestamp").String())}
	rec.payload = line.Get("payload")
	if !rec.payload.Exists() {
		rec.payload = line
	}
	if rec.kind == "" && line.Get("id").Exists() && (line.Get("cwd").Exists() || line.Get("instructions").Exists()) {
		rec.kind = "session_meta"
	}
	return rec, rec.kind != ""
}

func (r record) isItem() bool {
	return r.kind == "response_item" || slices.Contains(itemKinds, r.kind)
}

func messageText(content gjson.Result) string {
	if content.Type == gjson.String {
		return cleanText(content.String())
	}
	var parts []string
	content.ForEach(func(_, part gjson.Result) bool {
		switch part.Get("type").String() {
		case "input_text", "output_text", "text":
			if text := strings.TrimSpace(part.Get("text").String()); text != "" {
				parts = append(parts, text)
			}
		}
		return true
	})
	return cleanText(strings.Join(parts, "\n"))
}

func cleanText(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "<") {
		return ""
	}
	return text
}

var shells = []string{"sh", "bash", "zsh", "fish", "dash", "ksh"}

func commandString(command gjson.Result) string {
	if command.Type == gjson.String {
		return command.String()
	}
	var words []string
	command.ForEach(func(_, word gjson.Result) bool {
		words = append(words, word.String())
		return true
	})
	if len(words) >= 3 && slices.Contains(shells, filepath.Base(words[0])) {
		switch words[1] {
		case "-c", "-lc", "-ic", "-lic", "-ilc":
			return words[2]
		case "-l":
			if len(words) >= 4 && words[2] == "-c" {
				return words[3]
			}
		}
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = agent.ShellQuote(w)
	}
	return strings.Join(quoted, " ")
}

func callCommand(arguments gjson.Result) (command, cwd string) {
	if arguments.Type == gjson.String {
		arguments = gjson.Parse(arguments.String())
	}
	if c := arguments.Get("command"); c.Exists() {
		command = commandString(c)
	} else {
		command = arguments.Get("cmd").String()
	}
	return command, firstNonEmpty(arguments.Get("workdir").String(), arguments.Get("cwd").String(), arguments.Get("working_directory").String())
}

var codeModeCommand = regexp.MustCompile(`\bcmd\s*:\s*("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|` + "`(?:[^`\\\\]|\\\\.)*`" + `)`)

func codeModeCommands(input string) []string {
	var out []string
	for _, m := range codeModeCommand.FindAllStringSubmatch(input, -1) {
		literal := m[1]
		if unquoted, err := strconv.Unquote(literal); err == nil {
			literal = unquoted
		} else {
			literal = literal[1 : len(literal)-1]
		}
		if literal = strings.TrimSpace(literal); literal != "" {
			out = append(out, literal)
		}
	}
	return out
}

func fileURLPath(value string) string {
	if !strings.HasPrefix(value, "file://") {
		return value
	}
	u, err := url.Parse(value)
	if err != nil {
		return strings.TrimPrefix(value, "file://")
	}
	return u.Path
}

func sourceName(source gjson.Result) string {
	if source.IsObject() {
		for key := range source.Map() {
			return key
		}
	}
	return source.String()
}

func kindOf(source, originator, threadSource, agentRole string) string {
	lower := strings.ToLower(source + " " + threadSource)
	switch {
	case strings.Contains(lower, "sub") || agentRole != "":
		return "internal"
	case source == "exec" || strings.Contains(strings.ToLower(originator), "exec"):
		return "headless"
	}
	return "interactive"
}

func timestamp(value string) int64 {
	if value == "" {
		return 0
	}
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		if n < 1e12 {
			n *= 1000
		}
		return n
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.UnixMilli()
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
