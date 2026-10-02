package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tmux"
	"github.com/sadrishehu/hive/internal/tracker"
	"github.com/sadrishehu/hive/internal/tree"
)

// recentWindow is how far back `hive ls` looks without --all.
const recentWindow = 24 * time.Hour

func newLsCmd() *cobra.Command {
	var all, live, asJSON, noSync bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "Print the session tree",
		Long: "Print the session tree: trees with anything running, or active in the last 24h.\n" +
			"Agents can use --json to check on the sessions they spawned.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.Open(paths.DBPath())
			if err != nil {
				return err
			}
			defer st.Close()
			tr := tracker.New(st, adapters.All(), &tracker.System{})
			if !noSync {
				syncQuietly(cmd.Context(), tr)
			}
			sessions, err := tr.Refresh()
			if err != nil {
				return err
			}
			roots := tree.DropEmpty(tree.Build(sessions))
			switch {
			case live:
				roots = tree.Filter(roots, func(n *tree.Node) bool { return n.Session.Live() })
			case !all:
				roots = tree.Recent(roots, time.Now().Add(-recentWindow).UnixMilli())
			}
			if asJSON {
				return printJSON(roots)
			}
			printTree(roots, func(live int) string {
				return fmt.Sprintf("%d live · %d sessions", live, len(sessions))
			})
			hintInstall()
			return nil
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "every session ever recorded")
	cmd.Flags().BoolVarP(&live, "live", "l", false, "only running sessions and their ancestors")
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON: a flat list in tree order, with depth")
	cmd.Flags().BoolVar(&noSync, "no-sync", false, "skip importing history first; show only what hive already has")
	return cmd
}

func printJSON(roots []*tree.Node) error {
	type row struct {
		store.Session
		Live  bool `json:"live"`
		Depth int  `json:"depth"`
	}
	rows := []row{}
	var add func(nodes []*tree.Node, depth int)
	add = func(nodes []*tree.Node, depth int) {
		for _, n := range nodes {
			rows = append(rows, row{n.Session, n.Session.Live(), depth})
			add(n.Children, depth+1)
		}
	}
	add(roots, 0)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(rows)
}

type column struct {
	text  string
	style string // ANSI SGR parameters
}

// printTree prints the tree, then a footer saying how many of it are live.
func printTree(roots []*tree.Node, footer func(live int) string) {
	now := time.Now()
	var rows [][]column
	liveCount := 0
	tree.Walk(roots, func(n *tree.Node, prefix string) {
		s := n.Session
		live := s.Live()
		if live {
			liveCount++
		}
		glyph, glyphStyle := statusGlyph(s)
		dim := ""
		if !live {
			dim = "2"
		}
		rows = append(rows, []column{
			{prefix, "2"},
			{glyph, glyphStyle},
			{s.Tool, toolStyle(s.Tool)},
			{truncate(title(s), 48), dim},
			{kindTag(s), "2"},
			{statusWord(s), glyphStyle},
			{age(now, s.UpdatedAt), "2"},
			{shortPath(s.Cwd), "2"},
			{shortID(s), "2"},
		})
	})
	if len(rows) == 0 {
		fmt.Println("no sessions yet; try `hive ls --all`, or `hive install` to start tracking")
		return
	}
	// Pad every column except the tree prefix, which is part of the first cell.
	widths := make([]int, len(rows[0]))
	for _, r := range rows {
		lead := utf8.RuneCountInString(r[0].text) + utf8.RuneCountInString(r[1].text) + 1
		widths[2] = max(widths[2], lead+utf8.RuneCountInString(r[2].text))
		for i := 3; i < len(r); i++ {
			widths[i] = max(widths[i], utf8.RuneCountInString(r[i].text))
		}
	}
	color := useColor()
	for _, r := range rows {
		var b strings.Builder
		b.WriteString(paint(r[0].text, r[0].style, color))
		b.WriteString(paint(r[1].text, r[1].style, color))
		b.WriteByte(' ')
		lead := utf8.RuneCountInString(r[0].text) + utf8.RuneCountInString(r[1].text) + 1
		b.WriteString(paint(pad(r[2].text, widths[2]-lead), r[2].style, color))
		for i := 3; i < len(r); i++ {
			b.WriteString("  ")
			b.WriteString(paint(pad(r[i].text, widths[i]), r[i].style, color))
		}
		fmt.Println(strings.TrimRight(b.String(), " "))
	}
	fmt.Println(paint(footer(liveCount), "2", color))
}

// hintInstall points at `hive install` for agents on PATH that aren't connected.
func hintInstall() {
	var missing []string
	for _, a := range adapters.All() {
		if _, err := exec.LookPath(a.Spec().New[0]); err == nil && !a.Installed() {
			missing = append(missing, a.Spec().Name)
		}
	}
	if tmux.Available() && tmux.PopupKey(tmux.ConfPath()) == "" {
		missing = append(missing, "tmux (prefix+a)")
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "%s not connected yet: run `hive install`\n", strings.Join(missing, ", "))
	}
}

func statusGlyph(s store.Session) (string, string) {
	if !s.Live() {
		return "○", "2"
	}
	switch s.Status {
	case store.StatusWorking:
		return "●", "33"
	case store.StatusAttention:
		return "◆", "31;1"
	case store.StatusIdle:
		return "◉", "32"
	}
	return "◌", "34"
}

func statusWord(s store.Session) string {
	switch {
	case !s.Live():
		return "exited"
	case s.Status == store.StatusAttention:
		return "needs you"
	case s.Status == store.StatusUnknown && s.Source == "scan":
		return "untracked"
	case s.Status == store.StatusUnknown && s.Source == "pending":
		return "new"
	case s.Status == store.StatusUnknown:
		return "running"
	}
	return s.Status
}

func toolStyle(tool string) string {
	switch tool {
	case "claude":
		return "38;5;173"
	case "opencode":
		return "36"
	case "copilot":
		return "38;5;75"
	case "codex":
		return "38;5;35"
	}
	return "35"
}

func kindTag(s store.Session) string {
	switch s.Kind {
	case store.KindHeadless:
		return "run"
	case store.KindInternal:
		return "sub"
	}
	return ""
}

func title(s store.Session) string {
	for _, t := range []string{s.Title, s.LastPrompt} {
		if t = strings.Join(strings.Fields(t), " "); t != "" {
			return t
		}
	}
	switch s.Source {
	case "scan":
		return "(untracked)"
	case "pending", store.SourceLaunch:
		return "(new session)"
	}
	return "(untitled)"
}

func shortID(s store.Session) string {
	return s.Tool + ":" + truncateBare(s.NativeID, 12)
}

func shortPath(p string) string {
	if p == "" {
		return ""
	}
	if home := paths.Home(); strings.HasPrefix(p, home) {
		p = "~" + p[len(home):]
	}
	if utf8.RuneCountInString(p) <= 32 {
		return p
	}
	parts := strings.Split(p, "/")
	if len(parts) > 2 {
		return "…/" + strings.Join(parts[len(parts)-2:], "/")
	}
	return p
}

func age(now time.Time, ms int64) string {
	if ms == 0 {
		return ""
	}
	d := now.Sub(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(d.Seconds())))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return time.UnixMilli(ms).Format("Jan 2")
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func truncateBare(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func pad(s string, w int) string {
	return s + strings.Repeat(" ", max(0, w-utf8.RuneCountInString(s)))
}

func useColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func paint(s, style string, color bool) string {
	if !color || style == "" || s == "" {
		return s
	}
	return "\x1b[" + style + "m" + s + "\x1b[0m"
}
