package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
)

// waitPoll is how often `hive wait` looks again.
const waitPoll = 500 * time.Millisecond

func newWaitCmd() *cobra.Command {
	var anyOne bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait <id>...",
		Short: "Wait until sessions finish their turn, need you, or end",
		Long: "Wait until each session is done with what it was given, then print its ID and how it\n" +
			"stands: idle (its turn is over), attention (it waits on you: a permission or a\n" +
			"question) or exited. A message hive gave it (`hive new -p`, `send`, `resume -p`)\n" +
			"counts once the agent has answered it, so `hive wait` right after `hive send` waits\n" +
			"for the reply, not for the turn before it.\n\n" +
			"Sessions print as they get there; --any returns at the first. A session whose agent\n" +
			"doesn't report its turns (untracked, or a tool without hooks) is waited on until it ends.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, tr, sessions, err := openTracker()
			if err != nil {
				return err
			}
			defer st.Close()
			var pending []string
			for _, ref := range args {
				s, err := find(cmd.Context(), tr, sessions, ref)
				if err != nil {
					return err
				}
				if s.Live() && s.Status == store.StatusUnknown && s.Source != "pending" {
					fmt.Fprintf(os.Stderr, "%s doesn't report its turns to hive; waiting until it ends\n", s.ID)
				}
				if !slices.Contains(pending, s.ID) {
					pending = append(pending, s.ID)
				}
			}
			ctx := cmd.Context()
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			for {
				var still []string
				for _, id := range pending {
					state, done := store.StatusExited, true
					// A stand-in ID finds its session once the agent reports in;
					// one that is gone ended before it did.
					s, err := tracker.Lookup(sessions, id)
					switch {
					case errors.Is(err, tracker.ErrNoSession):
					case err != nil:
						return err
					default:
						if state, done, err = tr.Done(s); err != nil {
							return err
						}
						id = s.ID
					}
					if !done {
						still = append(still, id)
						continue
					}
					fmt.Println(id, state)
					if anyOne {
						return nil
					}
				}
				if pending = still; len(pending) == 0 {
					return nil
				}
				select {
				case <-ctx.Done():
					if errors.Is(ctx.Err(), context.DeadlineExceeded) {
						return fmt.Errorf("still busy after %s: %s", timeout, strings.Join(pending, ", "))
					}
					return ctx.Err()
				case <-time.After(waitPoll):
				}
				tr.World.Forget()
				if sessions, err = tr.Refresh(); err != nil {
					return err
				}
			}
		},
	}
	cmd.Flags().BoolVar(&anyOne, "any", false, "return when the first of them is done")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "give up after this long (0: wait as long as it takes)")
	return cmd
}
