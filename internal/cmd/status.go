package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
)

func newStatusCmd() *cobra.Command {
	var forTmux bool
	var then string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "One line for a status bar: how many agents need you, work, or are idle",
		Long: "Print how many running agents need you (◆), are working (●) or are idle (◉), leaving\n" +
			"out the ones at zero, and nothing at all when no agent runs.\n\n" +
			"For tmux's status line, add #(hive status --tmux) to the end of status-right in\n" +
			"tmux.conf; `hive install tmux` prints the line with your current right side in it.\n" +
			"Set the whole value: `set -ag` would add another copy each time tmux.conf is loaded.\n\n" +
			"--tmux colors it with tmux's own style tags. tmux runs it again every status-interval\n" +
			"(15s unless set), so a lower one shows changes sooner.\n\n" +
			"Claude Code sends its rate limits only to its status line. If you already have one\n" +
			"(say ~/.claude/statusline.py), set Claude Code's statusLine.command to\n" +
			"`hive status --then ~/.claude/statusline.py`: hive records the limits for the team\n" +
			"channel, then runs your line with the same JSON and prints what it prints.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			code, err := runStatus(cmd, forTmux, then)
			if err != nil {
				return err
			}
			if code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&forTmux, "tmux", false, "color it with tmux style tags, for the status line")
	cmd.Flags().StringVar(&then, "then", "", "run this status-line command with the same JSON, after recording its rate limits")
	return cmd
}

// runStatus is the status command without the exit: it returns the code a
// --then line brings from the user's own command, which the caller exits with.
func runStatus(cmd *cobra.Command, forTmux bool, then string) (int, error) {
	// Claude Code pipes its status JSON into the status line; read it first,
	// so a --then line works even when hive can't open its own database.
	data := readStatusInput()
	if then != "" {
		// The user's status line must not depend on hive: record what we
		// can, ignoring any error, then run it and keep its line.
		recordRateLimits(data)
		return runThen(then, data, cmd.OutOrStdout(), cmd.ErrOrStderr()), nil
	}
	st, _, sessions, err := openTracker()
	if err != nil {
		return 0, err
	}
	defer st.Close()
	captureRateLimits(st, data)
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
	return 0, nil
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

// statusInputWait is how long readStatusInput waits for more status JSON
// once the pipe stops sending: a caller that keeps stdin open and writes
// nothing must not hang the status line.
const statusInputWait = 200 * time.Millisecond

// readStatusInput reads the status JSON Claude Code writes to the status
// line's stdin. It returns nil when stdin is a terminal (a person runs it),
// or when the pipe stays open without sending for statusInputWait.
func readStatusInput() []byte {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	var (
		mu   sync.Mutex
		buf  bytes.Buffer
		seen atomic.Int64
		done = make(chan struct{})
	)
	go func() {
		defer close(done)
		tmp := make([]byte, 4096)
		for seen.Load() < 1<<20 {
			n, err := os.Stdin.Read(tmp)
			if n > 0 {
				mu.Lock()
				buf.Write(tmp[:n])
				mu.Unlock()
				seen.Add(int64(n))
			}
			if err != nil {
				return
			}
		}
	}()
	timer := time.NewTimer(statusInputWait)
	defer timer.Stop()
	last := int64(0)
	take := func() []byte {
		mu.Lock()
		defer mu.Unlock()
		return append([]byte(nil), buf.Bytes()...)
	}
	for {
		select {
		case <-done:
			return take()
		case <-timer.C:
			if n := seen.Load(); n == last {
				return take()
			} else {
				last = n
				timer.Reset(statusInputWait)
			}
		}
	}
}

// captureRateLimits records the rate limits the status JSON carries, for the
// channel view. It does nothing when no JSON came or the tool sent none.
// Claude is the tool that reports them this way, so they go under its name.
func captureRateLimits(st *store.Store, data []byte) {
	if len(data) == 0 {
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
	if q.Empty() {
		return
	}
	// Claude Code redraws often; don't rewrite the same windows.
	if old, ok, err := st.Quota(q.Tool); err == nil && ok &&
		old.FiveUsed == q.FiveUsed && old.FiveReset == q.FiveReset &&
		old.SevenUsed == q.SevenUsed && old.SevenReset == q.SevenReset {
		return
	}
	st.SetQuota(q)
}

// runThen runs the user's own status-line command with the JSON hive read,
// and returns its exit status, so the status line looks exactly as it did.
// Its stdout and stderr go straight out.
func runThen(command string, data []byte, stdout, stderr io.Writer) int {
	c := exec.Command("sh", "-c", command)
	c.Stdin = bytes.NewReader(data)
	c.Stdout, c.Stderr = stdout, stderr
	if err := c.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		fmt.Fprintln(stderr, "hive status --then:", err)
		return 1
	}
	return 0
}

// recordRateLimits stores the rate limits in the JSON with a plain database
// open, and ignores any error: the user's own status line must still draw.
func recordRateLimits(data []byte) {
	if len(data) == 0 {
		return
	}
	st, err := store.Open(paths.DBPath())
	if err != nil {
		return
	}
	defer st.Close()
	captureRateLimits(st, data)
}
