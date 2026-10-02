package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/store"
)

func newStatusCmd() *cobra.Command {
	var forTmux bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "One line for a status bar: how many agents need you, work, or are idle",
		Long: "Print how many running agents need you (◆), are working (●) or are idle (◉), leaving\n" +
			"out the ones at zero, and nothing at all when no agent runs. For tmux's status line:\n\n" +
			"  set -ag status-right ' #(hive status --tmux)'\n\n" +
			"--tmux colors it with tmux's own style tags. tmux runs it again every status-interval\n" +
			"(15s unless set), so a lower one shows changes sooner.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, _, sessions, err := openTracker()
			if err != nil {
				return err
			}
			defer st.Close()
			style := ansiStyles
			switch {
			case forTmux:
				style = tmuxStyles
			case !useColor():
				style = nil
			}
			if line := statusText(sessions, style); line != "" {
				fmt.Println(line)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&forTmux, "tmux", false, "color it with tmux style tags, for the status line")
	return cmd
}

// A count's style, around its text: for each of attention, working and idle.
type styles map[string][2]string

var (
	ansiStyles = styles{
		store.StatusAttention: {"\x1b[31;1m", "\x1b[0m"},
		store.StatusWorking:   {"\x1b[33m", "\x1b[0m"},
		store.StatusIdle:      {"\x1b[32m", "\x1b[0m"},
	}
	// Each undoes only what it set, so the line keeps the style around it.
	tmuxStyles = styles{
		store.StatusAttention: {"#[fg=red,bold]", "#[fg=default,nobold]"},
		store.StatusWorking:   {"#[fg=yellow]", "#[fg=default]"},
		store.StatusIdle:      {"#[fg=green]", "#[fg=default]"},
	}
)

// statusText counts the running agents by what they are doing, as "◆1 ●2 ◉1".
// A tool's own subagents count only when they need you; otherwise their work
// is their parent's.
func statusText(sessions []store.Session, style styles) string {
	counts := map[string]int{}
	for _, s := range sessions {
		switch {
		case !s.Live():
		case s.Status == store.StatusAttention:
			counts[s.Status]++
		case s.Kind == store.KindInternal:
		case s.Status == store.StatusWorking, s.Status == store.StatusIdle:
			counts[s.Status]++
		}
	}
	var parts []string
	for _, c := range []struct{ status, glyph string }{
		{store.StatusAttention, "◆"}, {store.StatusWorking, "●"}, {store.StatusIdle, "◉"},
	} {
		if n := counts[c.status]; n > 0 {
			around := style[c.status]
			parts = append(parts, fmt.Sprintf("%s%s%d%s", around[0], c.glyph, n, around[1]))
		}
	}
	return strings.Join(parts, " ")
}
