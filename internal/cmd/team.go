package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
	"github.com/sadrishehu/hive/internal/tmux"
	"github.com/sadrishehu/hive/internal/tracker"
)

// inboxPoll is how often `hive team inbox --wait` looks again.
const inboxPoll = time.Second

func newTeamCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "team",
		Short: "Agents that work on one goal together, and the channel they report in",
		Long: "A team is a named group of agents working on one goal in one folder. Each member is a\n" +
			"real session in its own tmux window; hive keeps who is on the team, the channel they\n" +
			"post their progress to, and the messages between them. A message to a member that is\n" +
			"working waits, and is typed into its pane when its turn ends.\n\n" +
			"Members are addressed by role (lead, backend, …). Inside a member, the team and the\n" +
			"role are implied.",
	}
	cmd.AddCommand(newTeamNewCmd(), newTeamLsCmd(), newTeamBoardCmd(), newTeamSayCmd(), newTeamPostCmd(),
		newTeamLogCmd(), newTeamInboxCmd(), newTeamAddCmd(), newTeamRmCmd(), newTeamStopCmd(),
		newTeamPauseCmd(true), newTeamPauseCmd(false), newTeamLeadCmd(), newTeamDeliverCmd(), newTeamDoneCmd())
	cmd.AddCommand(newTeamTaskCmd(), newTeamWaitCmd(), newTeamMuteCmd(true), newTeamMuteCmd(false))
	return cmd
}

// teamEnv is what a team command works with.
type teamEnv struct {
	st       *store.Store
	tr       *tracker.Tracker
	sessions []store.Session
	caller   string         // the session running the command, when an agent does
	roles    []tracker.Role // what the caller plays, when a team member runs the command
}

func openTeamEnv() (*teamEnv, error) {
	st, tr, sessions, err := openTracker()
	if err != nil {
		return nil, err
	}
	e := &teamEnv{st: st, tr: tr, sessions: sessions, caller: tr.Caller()}
	if e.roles, err = tr.CallerRoles(); err != nil {
		st.Close()
		return nil, err
	}
	return e, nil
}

// team resolves ref, or the caller's team, or the only active one.
func (e *teamEnv) team(ref string) (store.Team, error) {
	switch {
	case ref != "":
		return e.st.Team(ref)
	case len(e.roles) == 1:
		return e.roles[0].Team, nil
	case len(e.roles) > 1:
		var teams []store.Team
		for _, r := range e.roles {
			teams = append(teams, r.Team)
		}
		return store.Team{}, errors.New("you are on more than one team; name it: " + teamNames(teams))
	}
	teams, err := e.st.Teams()
	if err != nil {
		return store.Team{}, err
	}
	var active []store.Team
	for _, t := range teams {
		if t.Status == store.TeamActive {
			active = append(active, t)
		}
	}
	switch len(active) {
	case 0:
		if len(teams) > 0 { // all done
			return store.Team{}, errors.New("no team is active; name one: " + teamNames(teams))
		}
		return store.Team{}, errors.New("there is no team; `hive team new` starts one")
	case 1:
		return active[0], nil
	}
	return store.Team{}, errors.New("name the team: " + teamNames(active))
}

func teamNames(teams []store.Team) string {
	var names []string
	for _, t := range teams {
		names = append(names, t.Name)
	}
	return strings.Join(names, ", ")
}

// sender is who a message is from: --as, else the caller's role on the team.
// An agent that isn't on the team writes as itself, so that nobody takes it
// for the user: as team/role when it is on another team, else as its
// session. Only a command run outside any agent writes as the user.
func (e *teamEnv) sender(tm store.Team, as string) string {
	if as != "" {
		return as
	}
	for _, r := range e.roles {
		if r.Team.ID == tm.ID {
			return r.Member.Role
		}
	}
	if len(e.roles) > 0 {
		return e.roles[0].Team.Name + "/" + e.roles[0].Member.Role
	}
	if e.caller != "" {
		return e.caller
	}
	return store.FromYou
}

func (e *teamEnv) session(m store.Member) (store.Session, bool) {
	for _, s := range e.sessions {
		if m.SessionID != "" && s.ID == m.SessionID {
			return s, true
		}
	}
	return store.Session{}, false
}

func newTeamNewCmd() *cobra.Command {
	var goal, cwd, lead, from string
	var members []string
	var worktrees, wait bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "new <name>",
		Short: "Start a team: its members open in their own tmux windows",
		Long: "Record a team and start its members, each in its own tmux window with a brief: the goal,\n" +
			"its part, the roster and how to use `hive team`.\n\n" +
			"--lead picks who leads: a tool (claude, claude:auto, codex:gpt-…), self (the agent running\n" +
			"this command) or none (you lead from the tree). A member is role=tool or role=tool:model;\n" +
			"the model can be auto. --goal - reads the goal from stdin.\n\n" +
			"--from takes the goal, lead, worktrees and members of a [[team]] template in config.toml;\n" +
			"a flag you also pass wins, and --member adds to the template's members.\n\n" +
			"--worktrees gives each member a git worktree and branch of its own, hive/<team>/<role>,\n" +
			"made from the folder's HEAD; the lead works in the folder itself and merges them.\n" +
			"--wait waits until each member reports its session, so the team's roster is ready for\n" +
			"`hive send` and the like; a tool that starts its session only with its first message\n" +
			"gets a stand-in ID.",
		Example: "  hive team new api-v2 --goal \"add the admin API\" --lead claude \\\n" +
			"    --member backend=claude:auto --member frontend=codex --member reviewer=opencode\n" +
			"  hive team new api-v2 --from api --member reviewer=opencode",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !tmux.Available() {
				return errors.New("hive opens agents in tmux, which isn't on PATH")
			}
			if goal == "-" {
				in, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				goal = strings.TrimSpace(string(in))
			}
			var specs []team.Spec
			for _, m := range members {
				sp, err := team.ParseSpec(m)
				if err != nil {
					return err
				}
				specs = append(specs, sp)
			}
			if from != "" {
				cfg, err := adapters.LoadConfig(paths.ConfigPath())
				if err != nil {
					return err
				}
				t, err := findTeamTemplate(cfg, from)
				if err != nil {
					return err
				}
				changed := map[string]bool{
					"goal":      cmd.Flags().Changed("goal"),
					"lead":      cmd.Flags().Changed("lead"),
					"cwd":       cmd.Flags().Changed("cwd"),
					"worktrees": cmd.Flags().Changed("worktrees"),
				}
				if goal, lead, cwd, worktrees, specs, err = templateTeam(t, goal, lead, cwd, worktrees, changed, specs); err != nil {
					return err
				}
			}
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			if cwd == "" {
				if cwd, err = os.Getwd(); err != nil {
					return err
				}
			}
			o := tracker.TeamOptions{Name: args[0], Goal: goal, Cwd: cwd, Members: specs, CreatedBy: e.caller, Worktrees: worktrees}
			switch lead {
			case "none":
			case "self":
				s, ok, _ := e.st.Get(o.CreatedBy)
				if !ok {
					return errors.New("--lead self needs an agent to run the command; run it inside one, or pick a tool")
				}
				o.SelfLead = &s
			default:
				tool, model, _ := strings.Cut(lead, ":")
				o.Lead = &team.Spec{Role: team.LeadRole, Tool: tool, Model: model}
			}
			before := time.Now().UnixMilli()
			tm, launched, err := e.tr.NewTeam(cmd.Context(), o)
			if err != nil {
				return err
			}
			if wait {
				waitLaunched(cmd.Context(), e, tm, launched, before, timeout)
			}
			printLaunched(tm, launched)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&goal, "goal", "", "what the team is for; - reads it from stdin")
	f.StringVar(&cwd, "cwd", "", "folder the team works in (default: the current one)")
	_ = cmd.MarkFlagDirname("cwd")
	f.StringVar(&lead, "lead", "claude", "who leads: a tool[:model], self, or none (you lead from the tree)")
	f.StringArrayVar(&members, "member", nil, "a member: role=tool[:model]; repeat for each")
	f.StringVar(&from, "from", "", "a [[team]] template in config.toml: its goal, lead, worktrees and members")
	f.BoolVar(&worktrees, "worktrees", false, "a git worktree and branch per member; the lead works in the folder and merges")
	f.BoolVarP(&wait, "wait", "w", false, "wait until each member reports in, then print the roster with its session IDs")
	f.DurationVar(&timeout, "timeout", time.Minute, "how long --wait waits for each member")
	return cmd
}

// findTeamTemplate returns the [[team]] template named name, or says which
// templates the config has.
func findTeamTemplate(cfg adapters.Config, name string) (adapters.Team, error) {
	for _, t := range cfg.Teams {
		if t.Name == name {
			return t, nil
		}
	}
	var names []string
	for _, t := range cfg.Teams {
		names = append(names, t.Name)
	}
	if len(names) == 0 {
		return adapters.Team{}, fmt.Errorf("no team template %q in config.toml; add a [[team]] table", name)
	}
	return adapters.Team{}, fmt.Errorf("no team template %q in config.toml; templates: %s", name, strings.Join(names, ", "))
}

// templateTeam merges a template with the flags: the template gives defaults,
// a flag that was set wins, and --member values follow the template's members.
func templateTeam(t adapters.Team, goal, lead, cwd string, worktrees bool, changed map[string]bool, members []team.Spec) (string, string, string, bool, []team.Spec, error) {
	if t.Lead == "self" {
		return "", "", "", false, nil, fmt.Errorf("team template %q: a template can't lead as self; use a tool[:model] or none", t.Name)
	}
	if tool, _, _ := strings.Cut(t.Lead, ":"); t.Lead != "" && t.Lead != "none" && tool == "" {
		return "", "", "", false, nil, fmt.Errorf("team template %q: lead %q has no tool", t.Name, t.Lead)
	}
	if !changed["goal"] {
		goal = t.Goal
	}
	if !changed["lead"] && t.Lead != "" {
		lead = t.Lead
	}
	if !changed["cwd"] {
		cwd = t.Cwd
	}
	if !changed["worktrees"] {
		worktrees = t.Worktrees
	}
	specs := make([]team.Spec, 0, len(t.Members)+len(members))
	for _, m := range t.Members {
		specs = append(specs, team.Spec{Role: m.Role, Tool: m.Tool, Model: m.Model, Prompt: m.Prompt})
	}
	specs = append(specs, members...)
	return goal, lead, cwd, worktrees, specs, nil
}

// waitLaunched waits for each member hive started to report its session, and
// puts the ID it found in launched, so the roster prints it. A member that
// doesn't report in before timeout keeps the pane it was started in; the
// others are still printed.
func waitLaunched(ctx context.Context, e *teamEnv, tm store.Team, launched []tracker.MemberLaunch, after int64, timeout time.Duration) {
	for i := range launched {
		if ctx.Err() != nil {
			return
		}
		l := &launched[i]
		if l.Err != nil || l.Launched.SessionID != "" || l.Launched.Pane == "" {
			continue
		}
		s, err := e.tr.WaitForSession(l.Member.Tool, l.Launched.Pane, after, quietStart, timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", l.Member.Role, err)
			continue
		}
		l.Launched.SessionID = s.ID
	}
}

func printLaunched(tm store.Team, launched []tracker.MemberLaunch) {
	started := 0
	for _, l := range launched {
		if l.Err == nil {
			started++
		}
	}
	fmt.Printf("team %s: %d of %d members started in %s\n", tm.Name, started, len(launched), homePath(tm.Cwd))
	printWorkspace(tm)
	for _, l := range launched {
		m := l.Member
		var note string
		switch {
		case l.Err != nil:
			note = "failed: " + l.Err.Error()
		case l.Launched.Choice != nil:
			note = fmt.Sprintf("%s  pane %s  picked %s", orID(l.Launched.SessionID), l.Launched.Pane, l.Launched.Choice.Reason)
		case l.Launched.Pane == "":
			note = orID(l.Launched.SessionID)
		case l.Launched.SessionID == "":
			note = fmt.Sprintf("pane %s  reports in with its first event", l.Launched.Pane)
		default:
			note = fmt.Sprintf("%s  pane %s", l.Launched.SessionID, l.Launched.Pane)
		}
		fmt.Printf("  %-9s %-8s %s\n", m.Role, m.Tool, note)
	}
}

func orID(id string) string {
	if id == "" {
		return "starting"
	}
	return id
}

func newTeamLsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the teams, with their members, state and tasks done",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			teams, err := e.st.Teams()
			if err != nil {
				return err
			}
			type memberRow struct {
				store.Member
				State string `json:"state"`
			}
			type teamRow struct {
				store.Team
				Members []memberRow `json:"members"`
				Tasks   team.Counts `json:"tasks"`
			}
			rows := []teamRow{}
			for _, t := range teams {
				ms, err := e.st.Members(t.ID)
				if err != nil {
					return err
				}
				tasks, err := e.st.Tasks(t.ID)
				if err != nil {
					return err
				}
				row := teamRow{Team: t, Members: []memberRow{}, Tasks: team.Count(tasks)}
				for _, m := range ms {
					s, ok := e.session(m)
					row.Members = append(row.Members, memberRow{m, tracker.MemberState(m, s, ok)})
				}
				rows = append(rows, row)
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			if len(rows) == 0 {
				fmt.Println("no teams; `hive team new` starts one")
			}
			for _, r := range rows {
				state := r.Status
				if r.Paused {
					state += ", paused"
				}
				if r.Tasks.Total > 0 {
					state += fmt.Sprintf(" · %d/%d tasks done", r.Tasks.Done, r.Tasks.Total)
				}
				fmt.Printf("%s · %s · %s\n", r.Name, state, truncate(team.Clean(r.Goal), 60))
				for _, m := range r.Members {
					if m.Active() {
						fmt.Printf("  %-10s %-9s %s\n", m.Role, m.Tool, m.State)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON: the teams with their members")
	return cmd
}

func newTeamBoardCmd() *cobra.Command {
	var asJSON bool
	var n int
	cmd := &cobra.Command{
		Use:   "board [team]",
		Short: "The goal, who is on the team and doing what, the tasks, and the latest channel posts",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			tm, err := e.team(first(args))
			if err != nil {
				return err
			}
			ms, err := e.st.Members(tm.ID)
			if err != nil {
				return err
			}
			msgs, err := feed(e.st, tm.ID, n)
			if err != nil {
				return err
			}
			counts, _ := e.st.MessageCount(tm.ID)
			tasks, err := e.st.Tasks(tm.ID)
			if err != nil {
				return err
			}
			type memberRow struct {
				store.Member
				State    string `json:"state"`
				Messages int    `json:"messages"`
				Lead     bool   `json:"lead,omitempty"`
			}
			rows := []memberRow{}
			for _, m := range ms {
				s, ok := e.session(m)
				rows = append(rows, memberRow{m, tracker.MemberState(m, s, ok), counts[m.Role], m.Role == tm.LeadRole})
			}
			worktrees := teamWorktrees(e.tr, tm)
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(struct {
					store.Team
					Members   []memberRow     `json:"members"`
					Tasks     []taskRow       `json:"tasks"`
					Messages  []store.Message `json:"messages"`
					Worktrees *worktreeBoard  `json:"worktrees,omitempty"`
				}{tm, rows, taskRows(tasks), msgs, worktrees})
			}
			state := tm.Status
			if tm.Paused {
				state += ", paused"
			}
			lead := "you"
			if tm.LeadRole != "" {
				lead = tm.LeadRole
			}
			fmt.Printf("%s · %s · led by %s\n%s\n", tm.Name, state, lead, homePath(tm.Cwd))
			if tm.Goal != "" {
				fmt.Printf("goal: %s\n", team.Clean(tm.Goal))
			}
			fmt.Println("\nmembers")
			for _, m := range rows {
				if !m.Active() {
					continue
				}
				fmt.Printf("  %-10s %-9s %-9s %d posts", m.Role, m.Tool, m.State, m.Messages)
				if t, ok := team.Held(m.Role, tasks); ok {
					fmt.Printf("  #%d %s", t.N, truncate(t.Title, 40))
				}
				fmt.Println()
			}
			fmt.Println("\ntasks")
			printTasks(tasks, false)
			worktrees.print()
			fmt.Println("\nchannel")
			printMessages(msgs)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON: the team, its members, its tasks and the latest messages")
	cmd.Flags().IntVarP(&n, "lines", "n", 12, "how many channel messages")
	return cmd
}

func first(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// feed is the channel as the user reads it: private mail is read with inbox.
func feed(st *store.Store, teamID string, n int) ([]store.Message, error) {
	return st.Messages(teamID, n, false)
}

func printMessages(msgs []store.Message) {
	if len(msgs) == 0 {
		fmt.Println("  (nothing yet)")
	}
	for _, m := range msgs {
		who := team.Clean(m.From)
		if m.To != "" {
			who += " → " + team.Clean(m.To)
		}
		fmt.Printf("  %s  %-22s %s\n", time.UnixMilli(m.At).Format("15:04"), who, strings.ReplaceAll(team.Clean(m.Body), "\n", "\n"+strings.Repeat(" ", 31)))
	}
}

func newTeamSayCmd() *cobra.Command {
	var as, ref string
	var private bool
	cmd := &cobra.Command{
		Use:   "say <role|all> [text...]",
		Short: "Message a teammate or everyone; it is typed into their pane when they are idle",
		Long: "Write to a teammate (or all). If the recipient is idle in a pane, hive types the message there\n" +
			"now. If it is working or waiting on you, the message waits and is typed when its turn ends.\n" +
			"It also shows in the channel. --private keeps it out of the feed the user reads.\n" +
			"With no text, or \"-\", the message is read from stdin.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := readText(args[1:])
			if err != nil {
				return err
			}
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			tm, err := e.team(ref)
			if err != nil {
				return err
			}
			_, sent, err := e.tr.Say(tm, e.sender(tm, as), args[0], text, private)
			if err != nil {
				return err
			}
			for _, s := range sent {
				fmt.Fprintf(os.Stderr, "%s: %s\n", s.Role, s.How)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&ref, "team", "", "the team (default: the caller's)")
	f.StringVar(&as, "as", "", "send as this role, for scripts")
	f.BoolVar(&private, "private", false, "keep it out of the feed the user reads")
	return cmd
}

func newTeamPostCmd() *cobra.Command {
	var as, ref string
	cmd := &cobra.Command{
		Use:   "post [text...]",
		Short: "Post progress to the team's channel, which the user reads",
		Long: "Write to the channel. A post is logged for the user and typed into no pane, unless it names\n" +
			"a teammate with @role. With no text, or \"-\", it is read from stdin.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := readText(args)
			if err != nil {
				return err
			}
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			tm, err := e.team(ref)
			if err != nil {
				return err
			}
			_, sent, err := e.tr.Post(tm, e.sender(tm, as), text)
			if err != nil {
				return err
			}
			for _, s := range sent {
				fmt.Fprintf(os.Stderr, "%s: %s\n", s.Role, s.How)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ref, "team", "", "the team (default: the caller's)")
	cmd.Flags().StringVar(&as, "as", "", "post as this role, for scripts")
	return cmd
}

func newTeamLogCmd() *cobra.Command {
	var asJSON bool
	var n int
	cmd := &cobra.Command{
		Use:   "log [team]",
		Short: "The team's channel, oldest first",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			tm, err := e.team(first(args))
			if err != nil {
				return err
			}
			msgs, err := feed(e.st, tm.ID, n)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(msgs)
			}
			printMessages(msgs)
			return nil
		},
	}
	cmd.Flags().IntVarP(&n, "lines", "n", 40, "how many messages")
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON: the messages")
	return cmd
}

func newTeamInboxCmd() *cobra.Command {
	var wait bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Print the messages queued for this member, and mark them delivered",
		Long: "Print the messages hive hasn't typed into this member yet, for an agent that reads its mail\n" +
			"instead of waiting for it, from every team it is on. A paused team keeps its mail.\n" +
			"--wait blocks until one arrives.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			if len(e.roles) == 0 {
				return errors.New("inbox is for a team member; run it inside one")
			}
			deadline := time.Now().Add(timeout)
			for {
				got := 0
				for _, r := range e.roles {
					msgs, err := e.tr.Inbox(r.Team.ID, r.Member.Role)
					if err != nil {
						return err
					}
					for _, m := range msgs {
						fmt.Println(team.Envelope(r.Team.Name, m.From, m.Body))
					}
					got += len(msgs)
				}
				if got > 0 || !wait {
					return nil
				}
				if timeout > 0 && time.Now().After(deadline) {
					return fmt.Errorf("no message after %s", timeout)
				}
				time.Sleep(inboxPoll)
			}
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "wait for the next message")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "give up after this long (0: as long as it takes)")
	return cmd
}

func newTeamAddCmd() *cobra.Command {
	var prompt string
	cmd := &cobra.Command{
		Use:   "add <team> <role>=<tool>[:<model>]",
		Short: "Start one more member",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sp, err := team.ParseSpec(args[1])
			if err != nil {
				return err
			}
			sp.Prompt = prompt
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			tm, err := e.team(args[0])
			if err != nil {
				return err
			}
			m, l, err := e.tr.AddMember(cmd.Context(), tm, sp, "")
			if err != nil {
				return err
			}
			printLaunched(tm, []tracker.MemberLaunch{{Member: m, Launched: l}})
			return nil
		},
	}
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "the member's own part of the brief")
	return cmd
}

func newTeamRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <team> <role>",
		Short: "Retire a member: stop it and deliver no more mail to it; its history stays",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			tm, err := e.team(args[0])
			if err != nil {
				return err
			}
			return e.tr.RetireMember(tm, args[1])
		},
	}
}

func newTeamStopCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "stop <team>",
		Short: "Stop every member of a team",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			tm, err := e.team(args[0])
			if err != nil {
				return err
			}
			if !yes {
				ok, err := confirm("stop every member of "+tm.Name+"?", "this stops every member", "nothing stopped")
				if err != nil || !ok {
					return err
				}
			}
			n, err := e.tr.StopTeam(tm)
			fmt.Fprintf(os.Stderr, "stopped %d members of %s\n", n, tm.Name)
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask")
	return cmd
}

func newTeamPauseCmd(pause bool) *cobra.Command {
	use, short := "resume <team>", "Resume a paused team: queued mail is delivered, tasks can be claimed"
	if pause {
		use, short = "pause <team>", "Pause a team: no mail is delivered and no tasks are claimed until it resumes"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			tm, err := e.team(args[0])
			if err != nil {
				return err
			}
			return e.tr.SetPaused(tm, pause)
		},
	}
}

func newTeamMuteCmd(mute bool) *cobra.Command {
	use, short := "unmute <team> <role>", "Let hive deliver a member's mail again"
	if mute {
		use, short = "mute <team> <role>", "Pause hive's delivery to one member; its mail still queues, and inbox still reads it"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Long: "Stop hive from typing a member's queued mail into its pane, for two members that write to\n" +
			"each other without end. The mail still queues, and `hive team inbox` still reads it; a\n" +
			"paused team and a muted member both wait for their mail.\n\n" +
			"unmute lets delivery go on; what waited is typed at the member's next idle, as usual.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			tm, err := e.team(args[0])
			if err != nil {
				return err
			}
			role := strings.TrimPrefix(args[1], "@")
			if err := e.tr.MuteMember(tm, role, mute); err != nil {
				return err
			}
			if mute {
				fmt.Printf("%s on %s is muted: its mail waits until `hive team unmute %s %s`\n", role, tm.Name, tm.Name, role)
			} else {
				fmt.Printf("%s on %s is not muted; what waited is delivered at its next idle\n", role, tm.Name)
			}
			return nil
		},
	}
}

func newTeamLeadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lead <team> <role|none>",
		Short: "Change who leads; none means you lead from the tree",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			tm, err := e.team(args[0])
			if err != nil {
				return err
			}
			role := args[1]
			if role == "none" || role == store.FromYou {
				role = ""
			}
			return e.tr.SetLead(tm, role)
		},
	}
}

func newTeamDeliverCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "deliver <session>",
		Short:  "Type a member's queued messages into its pane (called by hooks)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, tr, sessions, err := openTracker()
			if err != nil {
				return err
			}
			defer st.Close()
			s, err := find(cmd.Context(), tr, sessions, args[0])
			if err != nil {
				return err
			}
			how, err := tr.Deliver(s.ID, tracker.Settle())
			if err != nil {
				logf("team deliver %s: %v", s.ID, err)
				return err
			}
			logf("team deliver %s: %s", s.ID, how)
			return nil
		},
	}
}

// startDeliver runs `hive team deliver` for a session as a process of its own,
// and returns at once: the hook that asks for it has seconds, not minutes.
func startDeliver(id string) {
	exe, err := os.Executable()
	if err != nil {
		logf("team deliver %s: %v", id, err)
		return
	}
	c := exec.Command(exe, "team", "deliver", id)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	c.Env = os.Environ()
	if err := c.Start(); err != nil {
		logf("team deliver %s: %v", id, err)
		return
	}
	c.Process.Release()
}
