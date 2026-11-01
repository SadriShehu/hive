package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/store"
)

func newStatusCmd() *cobra.Command {
	var forTmux bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "One line for a status bar: how many agents need you, work, or are idle",
		Long: "Print how many running agents need you (◆), are working (●) or are idle (◉), leaving\n" +
			"out the ones at zero, and nothing at all when no agent runs.\n\n" +
			"For tmux's status line, add #(hive status --tmux) to the end of status-right in\n" +
			"tmux.conf; `hive install tmux` prints the line with your current right side in it.\n" +
			"Set the whole value: `set -ag` would add another copy each time tmux.conf is loaded.\n\n" +
			"--tmux colors it with tmux's own style tags. tmux runs it again every status-interval\n" +
			"(15s unless set), so a lower one shows changes sooner.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, _, sessions, err := openTracker()
			if err != nil {
				return err
			}
			defer st.Close()
			// Claude Code pipes its status JSON into the status line; keep
			// the rate limits it reports for the channel view.
			captureRateLimits(st)
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

// rateWindow is one of Claude Code's rate-limit windows, as its status JSON
// reports it.
type rateWindow struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"`
}

// captureRateLimits reads the status JSON Claude Code writes to the status
// line's stdin, and records the rate limits it carries. It does nothing when
// stdin is a terminal (a person runs it), when no JSON comes, or when the
// tool sent none. Claude is the tool that reports them this way, so they go
// under its name.
func captureRateLimits(st *store.Store) {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil || len(data) == 0 {
		return
	}
	var payload struct {
		RateLimits *struct {
			Five  *rateWindow `json:"five_hour"`
			Seven *rateWindow `json:"seven_day"`
		} `json:"rate_limits"`
	}
	if json.Unmarshal(data, &payload) != nil || payload.RateLimits == nil {
		return
	}
	q := store.Quota{Tool: "claude", At: time.Now().Unix()}
	if w := payload.RateLimits.Five; w != nil {
		q.FiveUsed, q.FiveReset = w.UsedPercentage, w.ResetsAt
	}
	if w := payload.RateLimits.Seven; w != nil {
		q.SevenUsed, q.SevenReset = w.UsedPercentage, w.ResetsAt
	}
	if !q.Empty() {
		st.SetQuota(q)
	}
}
