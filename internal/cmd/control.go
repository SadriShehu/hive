package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tmux"
	"github.com/sadrishehu/hive/internal/tracker"
)

// The commands here are the tree's actions, for agents and scripts. Session
// IDs can be the full ID, the tool's own ID, or a unique prefix of either.

// openTracker opens the database with a fresh view of processes and panes.
// The caller closes the store.
func openTracker() (*store.Store, *tracker.Tracker, []store.Session, error) {
	st, err := store.Open(paths.DBPath())
	if err != nil {
		return nil, nil, nil, err
	}
	tr := newTracker(st)
	sessions, err := tr.Refresh()
	if err != nil {
		st.Close()
		return nil, nil, nil, err
	}
	return st, tr, sessions, nil
}

// find resolves ref, importing history first when it names a session hive
// hasn't seen yet.
func find(ctx context.Context, tr *tracker.Tracker, sessions []store.Session, ref string) (store.Session, error) {
	s, err := tracker.Lookup(sessions, ref)
	if !errors.Is(err, tracker.ErrNoSession) {
		return s, err
	}
	syncQuietly(ctx, tr)
	if sessions, err = tr.Refresh(); err != nil {
		return store.Session{}, err
	}
	return tracker.Lookup(sessions, ref)
}

// withSession runs fn on the session args[0] names.
func newTracker(st *store.Store) *tracker.Tracker {
	cfg := adapters.Current()
	tr := tracker.New(st, cfg.Adapters, &tracker.System{})
	tr.Models = cfg.Models
	return tr
}

func withSession(fn func(cmd *cobra.Command, tr *tracker.Tracker, s store.Session, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		st, tr, sessions, err := openTracker()
		if err != nil {
			return err
		}
		defer st.Close()
		s, err := find(cmd.Context(), tr, sessions, args[0])
		if err != nil {
			return err
		}
		return fn(cmd, tr, s, args[1:])
	}
}

// goTo shows pane: this tmux client switches to it, or, outside tmux, the
// terminal attaches to its session.
func goTo(pane string) error {
	if tmux.Inside() {
		return tmux.Focus(pane)
	}
	session, err := tmux.Run("display-message", "-p", "-t", pane, "#{session_name}")
	if err != nil {
		return err
	}
	if _, err := tmux.Run("select-window", "-t", pane); err != nil {
		return err
	}
	if _, err := tmux.Run("select-pane", "-t", pane); err != nil {
		return err
	}
	return tmux.Attach(session)
}

// quietStart is how long `hive new --wait` gives a running agent to report in
// before it settles for the pending session: some tools, like opencode without
// a prompt, start their session only with the first message.
const quietStart = 10 * time.Second

func newNewCmd() *cobra.Command {
	var cwd, prompt, parent string
	var wait, focus bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "new <tool>",
		Short: "Start an agent in its own tmux window and print its session ID",
		Long: "Start an agent's own TUI in a new tmux window, in the background unless --focus.\n" +
			"It is linked under the agent running this command (--parent auto), which can then\n" +
			"`hive send` to it, `hive tail` it and check on it with `hive ls --json`.\n\n" +
			"Claude and Copilot are given their session ID up front, so it prints at once. Other\n" +
			"tools choose their own, which --wait waits for; --wait also waits until the agent\n" +
			"is up, so a `hive send` right after it isn't typed before the agent can read it.\n" +
			"A tool that starts its session only with its first message gets a stand-in ID,\n" +
			"<tool>:pid-N, that names the session once it starts.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tool := args[0]
			if !tmux.Available() {
				return errors.New("hive opens agents in tmux, which isn't on PATH")
			}
			st, tr, sessions, err := openTracker()
			if err != nil {
				return err
			}
			defer st.Close()
			var parentID string
			switch parent {
			case "auto":
				parentID = tr.Caller()
			case "none":
			default:
				p, err := find(cmd.Context(), tr, sessions, parent)
				if err != nil {
					return err
				}
				parentID = p.ID
			}
			if cwd == "" {
				if cwd, err = os.Getwd(); err != nil {
					return err
				}
			}
			before := time.Now().UnixMilli()
			l, err := tr.Launch(tracker.LaunchOptions{Tool: tool, Cwd: cwd, Prompt: prompt,
				ParentID: parentID, Detached: !focus})
			if err != nil {
				return err
			}
			id := l.SessionID
			if wait {
				after := before - 1
				if s, ok, _ := st.Get(id); ok {
					after = s.StatusAt // registered by Launch; wait for the agent's own first event
				}
				s, err := tr.WaitForSession(tool, l.Pane, after, quietStart, timeout)
				if err != nil {
					return err
				}
				id = s.ID
				if s.Synthetic() {
					fmt.Fprintf(os.Stderr, "%s starts its session with its first message; until then it goes by %s, "+
						"which hive send, jump and kill accept, and which then names the session\n", tool, id)
				}
			}
			if id != "" {
				fmt.Println(id)
			} else {
				fmt.Fprintf(os.Stderr, "started %s in tmux pane %s; its ID comes with its first event (use --wait)\n", tool, l.Pane)
			}
			if focus {
				return goTo(l.Pane)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&cwd, "cwd", "", "folder to start in (default: the current one)")
	_ = cmd.MarkFlagDirname("cwd") // the shell completes folders
	f.StringVarP(&prompt, "prompt", "p", "", "the agent's first message")
	f.StringVar(&parent, "parent", "auto", "auto (the agent running this command), none, or a session ID")
	f.BoolVarP(&wait, "wait", "w", false, "wait until the agent is up, then print its session ID")
	f.BoolVar(&focus, "focus", false, "switch to the new window")
	f.DurationVar(&timeout, "timeout", time.Minute, "how long --wait waits")
	return cmd
}

func newJumpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "jump <id>",
		Short: "Switch to a session's pane, reopening it if it has ended",
		Args:  cobra.ExactArgs(1),
		RunE: withSession(func(cmd *cobra.Command, tr *tracker.Tracker, s store.Session, _ []string) error {
			if s.Pane != "" && tmux.PaneExists(s.Pane) {
				return goTo(s.Pane)
			}
			if s.Live() {
				return fmt.Errorf("it runs outside tmux (pid %d), where hive can't take you", s.PID)
			}
			l, err := tr.Resume(s, "", tracker.LaunchOptions{})
			if err != nil {
				return err
			}
			return goTo(l.Pane)
		}),
	}
}

func newResumeCmd() *cobra.Command {
	var prompt string
	var focus bool
	cmd := &cobra.Command{
		Use:   "resume <id>",
		Short: "Reopen an ended session in its own TUI",
		Long:  "Reopen an ended session in a new tmux window, in the background unless --focus.",
		Args:  cobra.ExactArgs(1),
		RunE: withSession(func(cmd *cobra.Command, tr *tracker.Tracker, s store.Session, _ []string) error {
			l, err := tr.Resume(s, prompt, tracker.LaunchOptions{Detached: !focus})
			if err != nil {
				return err
			}
			fmt.Println(s.ID)
			if focus {
				return goTo(l.Pane)
			}
			return nil
		}),
	}
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "a message to continue with")
	cmd.Flags().BoolVar(&focus, "focus", false, "switch to the reopened window")
	return cmd
}

func newSendCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "send <id> [text...]",
		Short: "Type a message into a session, or reopen it with the message",
		Long: "Type a message into a session's pane and press Enter. A session that has ended is\n" +
			"reopened in the background with the message. With no text, or \"-\", the message\n" +
			"is read from stdin.",
		Args: cobra.MinimumNArgs(1),
		RunE: withSession(func(cmd *cobra.Command, tr *tracker.Tracker, s store.Session, args []string) error {
			text := strings.Join(args, " ")
			if text == "" || text == "-" {
				in, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				text = strings.TrimRight(string(in), "\n")
			}
			result, err := tr.Send(s, text, tracker.LaunchOptions{Detached: true})
			if err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, result)
			return nil
		}),
	}
}

func newTailCmd() *cobra.Command {
	var n int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "tail <id>",
		Short: "Print the end of a session's transcript",
		Args:  cobra.ExactArgs(1),
		RunE: withSession(func(cmd *cobra.Command, tr *tracker.Tracker, s store.Session, _ []string) error {
			lines, err := tr.Tail(s, n)
			if err != nil {
				return err
			}
			if asJSON {
				type line struct {
					Role string `json:"role"`
					Text string `json:"text"`
				}
				out := []line{}
				for _, l := range lines {
					out = append(out, line{l.Role, l.Text})
				}
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}
			for _, l := range lines {
				text := strings.ReplaceAll(strings.TrimRight(l.Text, "\n"), "\n", "\n  ")
				fmt.Printf("%s: %s\n", l.Role, text)
			}
			return nil
		}),
	}
	cmd.Flags().IntVarP(&n, "lines", "n", 40, "how many entries")
	cmd.Flags().BoolVar(&asJSON, "json", false, `JSON: [{"role", "text"}]`)
	return cmd
}

func newUsageCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "usage <id>",
		Short: "Print what a session used: model, tokens, price, tools, skills, context",
		Long: "Print what a session used, read from the tool's own records: the model, token counts,\n" +
			"the price (reported by the tool, or estimated from built-in and [[model]] prices),\n" +
			"the tools and skills it called, and how much of its context window is in use.",
		Args: cobra.ExactArgs(1),
		RunE: withSession(func(cmd *cobra.Command, tr *tracker.Tracker, s store.Session, _ []string) error {
			u, err := tr.Usage(cmd.Context(), s)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(u)
			}
			if u.Empty() {
				fmt.Println("no usage recorded for this session")
				return nil
			}
			for _, line := range usageLines(s, u) {
				fmt.Println(line)
			}
			return nil
		}),
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON: the usage record")
	return cmd
}

func usageLines(s store.Session, u store.Usage) []string {
	var lines []string
	add := func(label, value string) {
		if value != "" {
			lines = append(lines, fmt.Sprintf("%-9s %s", label, value))
		}
	}
	add("model", joinParts(u.Model, u.Effort))
	add("requests", fmt.Sprint(u.Requests))
	add("tokens", tokenLine(u.Tokens))
	add("cost", costLine(s, u))
	add("context", contextLine(u))
	add("tools", countLine(u.Tools))
	add("skills", countLine(u.Skills))
	var more []string
	if u.Compactions > 0 {
		more = append(more, fmt.Sprintf("%d compactions", u.Compactions))
	}
	if u.LinesAdded > 0 || u.LinesRemoved > 0 {
		more = append(more, fmt.Sprintf("lines +%d −%d", u.LinesAdded, u.LinesRemoved))
	}
	if u.APIDurationMS > 0 {
		more = append(more, "api "+(time.Duration(u.APIDurationMS)*time.Millisecond).Round(time.Second).String())
	}
	if u.PremiumRequests > 0 {
		more = append(more, fmt.Sprintf("%g premium requests", u.PremiumRequests))
	}
	if u.Partial {
		more = append(more, "partial: totals come when the session ends")
	}
	add("more", strings.Join(more, " · "))
	return lines
}

func joinParts(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}

func tokenLine(t store.Tokens) string {
	parts := []string{fmt.Sprintf("in %d", t.Input), fmt.Sprintf("out %d", t.Output)}
	if t.CacheRead > 0 {
		parts = append(parts, fmt.Sprintf("cache read %d", t.CacheRead))
	}
	if t.CacheWrite > 0 {
		parts = append(parts, fmt.Sprintf("cache write %d", t.CacheWrite))
	}
	if t.Reasoning > 0 {
		parts = append(parts, fmt.Sprintf("reasoning %d", t.Reasoning))
	}
	return strings.Join(parts, " · ")
}

func costLine(s store.Session, u store.Usage) string {
	switch u.CostSource {
	case store.CostTool:
		return fmt.Sprintf("$%.2f reported by %s", u.CostUSD, s.Tool)
	case store.CostConfig:
		line := fmt.Sprintf("~$%.2f estimated from built-in and [[model]] prices", u.CostUSD)
		if u.Partial && s.Tool == "claude" {
			line += " · exact at exit"
		}
		return line
	}
	if u.Model == "" {
		return "unknown"
	}
	return fmt.Sprintf("unknown · add a [[model]] with prices for %s to config.toml", u.Model)
}

func contextLine(u store.Usage) string {
	switch {
	case u.ContextTokens == 0:
		return ""
	case u.ContextWindow > 0:
		return fmt.Sprintf("%d of %d (%d%%)", u.ContextTokens, u.ContextWindow, u.ContextTokens*100/u.ContextWindow)
	}
	return fmt.Sprintf("%d · window unknown", u.ContextTokens)
}

func countLine(counts map[string]int) string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s %d", name, counts[name]))
	}
	return strings.Join(parts, " · ")
}

func newKillCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kill <id>",
		Short: "Stop a session's process",
		Long:  "Stop a session's process. A window hive opened for it closes with it.",
		Args:  cobra.ExactArgs(1),
		RunE: withSession(func(cmd *cobra.Command, tr *tracker.Tracker, s store.Session, _ []string) error {
			if err := tr.Stop(s); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, "stopped", s.ID)
			return nil
		}),
	}
}
