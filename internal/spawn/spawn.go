// Package spawn finds agent invocations in shell commands, so a session's
// history shows which agents it started. It knows no tool in particular: any
// adapter's program found in any session's commands is a spawn.
package spawn

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/paths"
)

// Found is one agent a command starts.
type Found struct {
	Tool     string
	Title    string // from the tool's title flag
	NativeID string // from the tool's session flag
	Cwd      string // where it runs, following any "cd" before it
	Headless bool   // a non-interactive run
}

// wrappers run the command that follows them.
var wrappers = []string{"env", "nohup", "time", "exec", "command", "caffeinate", "nice", "stdbuf", "timeout", "gtimeout"}

// keywords can start a shell line and are followed by a command.
var keywords = []string{"do", "then", "else", "elif", "if", "while", "until", "{", "!"}

var (
	assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	duration   = regexp.MustCompile(`^[0-9.]+[smhd]?$`)
)

// Find returns the agents cmd starts. cwd is where cmd ran.
func Find(cmd, cwd string, specs []agent.Spec) []Found {
	var out []Found
	for _, words := range commands(cmd) {
		if words[0] == "cd" {
			cwd = chdir(cwd, words[1:])
			continue
		}
		i := 0
		for i < len(words) {
			w := words[i]
			if assignment.MatchString(w) || slices.Contains(keywords, w) {
				i++
				continue
			}
			if !slices.Contains(wrappers, filepath.Base(w)) {
				break
			}
			for i++; i < len(words) && (strings.HasPrefix(words[i], "-") || assignment.MatchString(words[i]) || duration.MatchString(words[i])); i++ {
			}
		}
		if i >= len(words) {
			continue
		}
		argv := words[i:]
		program := strings.TrimSuffix(filepath.Base(argv[0]), ".js")
		for _, spec := range specs {
			if !slices.Contains(spec.Process, program) || informational(argv) {
				continue
			}
			out = append(out, Found{
				Tool:     spec.Name,
				Title:    flagValue(argv, spec.TitleFlags),
				NativeID: flagValue(argv, spec.SessionFlags),
				Cwd:      cwd,
				Headless: spec.IsHeadless(argv),
			})
		}
	}
	return out
}

// informational reports whether argv only asks for help or a version.
func informational(argv []string) bool {
	for _, a := range argv[1:] {
		switch a {
		case "-h", "--help", "-v", "--version":
			return true
		}
	}
	return false
}

// flagValue returns the value of the first of flags in argv, as "--flag v"
// or "--flag=v".
func flagValue(argv []string, flags []string) string {
	for i, a := range argv[1:] {
		for _, f := range flags {
			if a == f && i+2 < len(argv) {
				return argv[i+2]
			}
			if v, ok := strings.CutPrefix(a, f+"="); ok {
				return v
			}
		}
	}
	return ""
}

// chdir follows `cd args` from cwd. Anything it can't resolve (variables,
// "cd -") leaves cwd as it was.
func chdir(cwd string, args []string) string {
	if len(args) == 0 {
		return paths.Home()
	}
	dir := args[0]
	switch {
	case strings.ContainsAny(dir, "$`") || dir == "-":
		return cwd
	case dir == "~":
		return paths.Home()
	case strings.HasPrefix(dir, "~/"):
		return filepath.Join(paths.Home(), dir[2:])
	case filepath.IsAbs(dir):
		return filepath.Clean(dir)
	case cwd == "":
		return ""
	}
	return filepath.Join(cwd, dir)
}

// commands splits a shell command line into simple commands, each a list of
// words with quotes removed. It is not a shell: it handles quoting, escapes,
// comments and the operators that separate commands, which is enough to find
// what a command runs.
func commands(s string) [][]string {
	var (
		out    [][]string
		words  []string
		cur    strings.Builder
		inWord bool
	)
	endWord := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	endCommand := func() {
		endWord()
		if len(words) > 0 {
			out = append(out, words)
			words = nil
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s):
			i++
			if s[i] != '\n' { // backslash-newline continues the line
				cur.WriteByte(s[i])
				inWord = true
			}
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				end = len(s) - i - 1
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case c == '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
			}
			inWord = true
		case c == ' ' || c == '\t':
			endWord()
		case c == '#' && !inWord:
			for i < len(s) && s[i] != '\n' {
				i++
			}
			endCommand()
		case c == '&' && ((i > 0 && s[i-1] == '>') || (i+1 < len(s) && s[i+1] == '>')):
			cur.WriteByte(c) // a redirection: 2>&1, &>
			inWord = true
		case strings.IndexByte("\n;|&()", c) >= 0:
			endCommand()
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	endCommand()
	return out
}
