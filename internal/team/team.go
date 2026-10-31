// Package team holds the rules of agent teams that need no database or
// terminal: who a role is, what a member is told when it starts, how a message
// is worded for another agent's prompt, and when it may be typed.
package team

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/sadrishehu/hive/internal/store"
)

// Environment variables every member's window carries, next to HIVE_PARENT.
const (
	TeamEnv = "HIVE_TEAM"
	RoleEnv = "HIVE_ROLE"
)

// LeadRole is the role of the agent hive starts to lead a team.
const LeadRole = "lead"

// Names that can't be roles: they address messages.
var reserved = map[string]bool{store.ToAll: true, store.FromYou: true, store.FromHive: true, "none": true, "self": true}

var rolePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,23}$`)

// ValidRole says why role can't be one, or returns nil.
func ValidRole(role string) error {
	switch {
	case reserved[role]:
		return fmt.Errorf("%q is a name hive uses; pick another role", role)
	case !rolePattern.MatchString(role):
		return fmt.Errorf("role %q must be lower-case letters, digits, - or _, starting with a letter (24 at most)", role)
	}
	return nil
}

// ValidName says why name can't be a team's, or returns nil.
func ValidName(name string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`).MatchString(name) {
		return fmt.Errorf("team name %q must be letters, digits, . - or _ (40 at most)", name)
	}
	return nil
}

// Spec is one member to start: a role, a tool, and a model.
type Spec struct {
	Role   string
	Tool   string // a tool, or "auto"
	Model  string // "" for the tool's default, or "auto"
	Prompt string // the member's own part of the brief
}

// ParseSpec reads "role=tool", "role=tool:model" or "role=tool:auto".
func ParseSpec(s string) (Spec, error) {
	role, rest, ok := strings.Cut(strings.TrimSpace(s), "=")
	if !ok || rest == "" {
		return Spec{}, fmt.Errorf("member %q must look like role=tool or role=tool:model", s)
	}
	if err := ValidRole(role); err != nil {
		return Spec{}, err
	}
	tool, model, _ := strings.Cut(rest, ":")
	if tool == "" {
		return Spec{}, fmt.Errorf("member %q has no tool", s)
	}
	return Spec{Role: role, Tool: tool, Model: model}, nil
}

// ParseSpecs reads a space-separated list of specs, as the new-team form
// takes them.
func ParseSpecs(line string) ([]Spec, error) {
	var out []Spec
	for _, f := range strings.Fields(line) {
		sp, err := ParseSpec(f)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, nil
}

// CheckRoles says why specs can't make a team: a role twice, or none.
func CheckRoles(specs []Spec) error {
	seen := map[string]bool{}
	for _, sp := range specs {
		if seen[sp.Role] {
			return fmt.Errorf("the role %q is on the team twice", sp.Role)
		}
		seen[sp.Role] = true
	}
	return nil
}

// Roster lists members as "role · tool".
func Roster(members []store.Member) string {
	var parts []string
	for _, m := range members {
		if m.Active() {
			parts = append(parts, m.Role+" · "+m.Tool)
		}
	}
	return strings.Join(parts, ", ")
}

// Brief is the first prompt of a member: the goal, its part, the roster, and
// how to work through hive. It is the one place hive talks to an agent.
func Brief(t store.Team, m store.Member, members []store.Member) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s on the hive team %s (%s), in %s.\n\n", m.Role, t.Name, Roster(members), Folder(t, m))
	if t.Goal != "" {
		fmt.Fprintf(&b, "Goal: %s\n", t.Goal)
	}
	if m.Prompt != "" {
		fmt.Fprintf(&b, "Your part: %s\n", m.Prompt)
	}
	b.WriteString("\nThe team works through hive. A human follows along in the team's channel.\n" +
		"- `hive team board` shows the goal, who is on the team, the tasks and the latest messages.\n" +
		"- Tasks: `hive team task claim <n>` takes one; `hive team task done <n> --note \"what changed\"`\n" +
		"  closes it; `hive team task block <n> --why \"…\"` says what stops it. hive posts each to the\n" +
		"  channel for you, and tells you when a task of yours is ready.\n" +
		"- Progress: `hive team post \"text\"` writes to the channel, which the human reads. Post when you\n" +
		"  finish a step that matters, when you are blocked, or when you learn something the others need.\n" +
		"  Not every step. `hive team log -n 20` shows the channel.\n" +
		"- Messages: `hive team say <role> \"text\"` writes to one teammate; `hive team say all \"text\"` to\n" +
		"  everyone. Messages to you arrive here as lines that start with [" + t.Name + " · from <role>].\n" +
		"  Answer them with `hive team say` or `hive team post`, not here.\n" +
		"- When you are done or blocked, say so with `hive team post`, then end your turn. hive wakes\n" +
		"  you here when someone writes to you.\n")
	b.WriteString(workLine(t, m, members))
	switch {
	case m.Role == t.LeadRole:
		b.WriteString("\nYou lead. Turn the goal into tasks, each with an owner and the tasks it waits for: " +
			"`hive team task add \"title\" --owner <role> --after <n,…>`. Review what is marked done, integrate it, " +
			"and post when the goal is met.\n")
	case t.LeadRole == "":
		b.WriteString("\nThe human leads this team from hive and writes to you in the channel. " +
			"Start by reading `hive team board`, then claim a task of yours, or wait for your part.\n")
	default:
		b.WriteString("\nStart with `hive team board`, then claim a task of yours, or wait for one. The lead is " + t.LeadRole + ".\n")
	}
	return b.String()
}

// flat puts text on one line of plain characters: a line break or any other
// control character, like the escape that ends a bracketed paste, becomes a
// space.
func flat(text string) string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text))
}

// Envelope words a message for the recipient's prompt. Every line starts with
// "[", so a message can't run a slash command, a shell escape or a memory
// shortcut in the recipient's TUI, and it holds no control character, so it
// can't end the paste early and reach the TUI as keys.
func Envelope(team, from, body string) string {
	return fmt.Sprintf("[%s · from %s] %s", team, flat(from), flat(body))
}

// Paste joins queued messages into the text typed into a pane, one line each.
func Paste(team string, msgs []store.Message) string {
	lines := make([]string, len(msgs))
	for i, m := range msgs {
		lines[i] = Envelope(team, m.From, m.Body)
	}
	return strings.Join(lines, "\n")
}

// How a message reaches a member.
type Delivery int

const (
	Queue  Delivery = iota // wait: the member is busy or the team is paused
	Type                   // type it into its pane now
	Blind                  // type it now, not knowing whether the member is ready
	Reopen                 // the member has ended: start it again with the message
)

func (d Delivery) String() string {
	return [...]string{"queue", "type", "type (blind)", "reopen"}[d]
}

// Decide says how a message reaches a member in a given state. inPane is
// whether the member's session runs in a tmux pane hive can type into.
func Decide(paused bool, status string, inPane bool) Delivery {
	if paused {
		return Queue
	}
	switch status {
	case store.StatusIdle:
		if inPane {
			return Type
		}
		return Queue
	case store.StatusWorking, store.StatusAttention:
		return Queue // typing into a permission prompt would answer it
	case store.StatusExited:
		return Reopen
	}
	if inPane {
		return Blind
	}
	return Queue
}

// Recipients lists the roles a message is addressed to: the role itself, or
// every active member but the sender for "all".
func Recipients(to, from string, members []store.Member) ([]string, error) {
	var out []string
	switch to {
	case "":
		return nil, nil
	case store.ToAll:
		for _, m := range members {
			if m.Active() && m.Role != from {
				out = append(out, m.Role)
			}
		}
		return out, nil
	}
	if to == from {
		return nil, fmt.Errorf("%s can't message itself", to)
	}
	for _, m := range members {
		if m.Role == to && m.Active() {
			return []string{to}, nil
		}
	}
	return nil, fmt.Errorf("the team has no %s; roles: %s", to, Roster(members))
}

// Address splits "@role text" into the role and the text. Without a leading
// @role it addresses the whole team.
func Address(text string) (to, body string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "@") {
		return store.ToAll, text
	}
	word, rest, _ := strings.Cut(text[1:], " ")
	if word == "" {
		return store.ToAll, text
	}
	if word == store.ToAll {
		return store.ToAll, strings.TrimSpace(rest)
	}
	return word, strings.TrimSpace(rest)
}

// Mentions lists the roles a post names with @role, in order, once each.
func Mentions(body string, members []store.Member) []string {
	var out []string
	for _, m := range members {
		if !m.Active() {
			continue
		}
		// Not \b, which ends a word at "-": @dev isn't in "@dev-ops".
		re := regexp.MustCompile(`(^|\s)@` + regexp.QuoteMeta(m.Role) + `([^A-Za-z0-9_-]|$)`)
		if re.MatchString(body) {
			out = append(out, m.Role)
		}
	}
	return out
}
