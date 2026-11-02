package cmd

import (
	"context"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
	"github.com/sadrishehu/hive/internal/tracker"
)

// The completions here read the database read-only and never touch tmux or the
// network, so a <TAB> stays quick even while agents are running.

// shortIDs gives every session the shortest form of its ID that is unique
// among the stored sessions of its tool: the tool, then the shortest prefix of
// the native ID, at least eight characters (not counting a leading "ses_"),
// as git shortens hashes. It stays a prefix of the full ID, so every command
// still accepts it.
func shortIDs(sessions []store.Session) map[string]string {
	byTool := map[string][]store.Session{}
	for _, s := range sessions {
		byTool[s.Tool] = append(byTool[s.Tool], s)
	}
	out := make(map[string]string, len(sessions))
	for tool, group := range byTool {
		sort.Slice(group, func(i, j int) bool { return group[i].NativeID < group[j].NativeID })
		for i, s := range group {
			lcp := 0
			if i > 0 {
				lcp = max(lcp, commonPrefix(s.NativeID, group[i-1].NativeID))
			}
			if i+1 < len(group) {
				lcp = max(lcp, commonPrefix(s.NativeID, group[i+1].NativeID))
			}
			least := 8
			if strings.HasPrefix(s.NativeID, "ses_") {
				least = len("ses_") + 8
			}
			n := min(max(least, lcp+1), len(s.NativeID))
			out[s.ID] = tool + ":" + s.NativeID[:n]
		}
	}
	return out
}

func commonPrefix(a, b string) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// shortID reads one session's short form out of the map. A session the caller
// didn't list gets the least form, still a prefix the commands accept.
func shortID(ids map[string]string, id string) string {
	if short, ok := ids[id]; ok {
		return short
	}
	return minShortID(id)
}

func minShortID(id string) string {
	if id == "" {
		return ""
	}
	tool, native, ok := strings.Cut(id, ":")
	if !ok {
		return id
	}
	least := 8
	if strings.HasPrefix(native, "ses_") {
		least = len("ses_") + 8
	}
	return tool + ":" + native[:min(least, len(native))]
}

func shortIDList(ids map[string]string, order []string) string {
	out := make([]string, len(order))
	for i, id := range order {
		out[i] = shortID(ids, id)
	}
	return strings.Join(out, ", ")
}

// compText puts text on one line for a completion description.
func compText(s string) string {
	return strings.Join(strings.Fields(team.Clean(s)), " ")
}

func compDesc(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = compText(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}

// openCompletionStore opens hive's database read-only. It returns false when
// there is none, so completion says nothing instead of making one.
func openCompletionStore() (*store.Store, bool) {
	if _, err := os.Stat(paths.DBPath()); err != nil {
		return nil, false
	}
	st, err := store.OpenRead(paths.DBPath())
	if err != nil {
		return nil, false
	}
	return st, true
}

// compData is what the completions read, loaded once per request.
type compData struct {
	st       *store.Store
	sessions []store.Session
	teams    []store.Team
	members  []store.Member
	tasks    map[string][]store.Task
	byID     map[string]store.Session
	held     map[string]store.Task // session ID -> the task it holds
	ids      map[string]string     // session ID -> its short form
}

func loadCompData(st *store.Store) *compData {
	d := &compData{st: st, tasks: map[string][]store.Task{}, byID: map[string]store.Session{}, held: map[string]store.Task{}}
	d.sessions, _ = st.All()
	d.teams, _ = st.Teams()
	d.members, _ = st.AllMembers()
	for _, t := range d.teams {
		tasks, _ := st.Tasks(t.ID)
		d.tasks[t.ID] = tasks
	}
	for _, s := range d.sessions {
		d.byID[s.ID] = s
	}
	d.ids = shortIDs(d.sessions)
	for _, m := range d.members {
		if m.SessionID == "" {
			continue
		}
		if h, ok := team.Held(m.Role, d.tasks[m.TeamID]); ok {
			d.held[m.SessionID] = h
		}
	}
	return d
}

func (d *compData) memberOf(sessionID string) (store.Member, bool) {
	for _, m := range d.members {
		if m.SessionID == sessionID {
			return m, true
		}
	}
	return store.Member{}, false
}

// memberState says what a member is doing without asking tmux, which
// completion must not call.
func (d *compData) memberState(m store.Member) string {
	if !m.Active() {
		return "left"
	}
	if m.SessionID == "" {
		return "starting"
	}
	s, ok := d.byID[m.SessionID]
	if !ok {
		return "starting"
	}
	if !s.Live() && s.Status != store.StatusUnknown {
		return "ended"
	}
	if s.Status == store.StatusAttention {
		return "needs you"
	}
	if s.Status == "" {
		return "starting"
	}
	return s.Status
}

// sessionValues lists the live sessions first and then those active in the
// last day, newest first in each.
func (d *compData) sessionValues() []string {
	cutoff := time.Now().Add(-24 * time.Hour).UnixMilli()
	var live, recent []store.Session
	for _, s := range d.sessions {
		switch {
		case s.Live():
			live = append(live, s)
		case s.UpdatedAt >= cutoff:
			recent = append(recent, s)
		}
	}
	newest := func(xs []store.Session) {
		sort.SliceStable(xs, func(i, j int) bool { return xs[i].UpdatedAt > xs[j].UpdatedAt })
	}
	newest(live)
	newest(recent)
	out := make([]string, 0, len(live)+len(recent))
	for _, s := range append(live, recent...) {
		if strings.Contains(s.NativeID, "/") {
			continue // a tool's own subagent: its ID can't be sent to
		}
		out = append(out, shortID(d.ids, s.ID)+"\t"+d.sessionDesc(s))
	}
	return out
}

func (d *compData) sessionDesc(s store.Session) string {
	parts := []string{s.Tool}
	if m, ok := d.memberOf(s.ID); ok {
		parts = append(parts, m.Role)
	}
	if h, ok := d.held[s.ID]; ok {
		parts = append(parts, "#"+strconv.Itoa(h.N)+" "+h.Title)
	} else if t := title(s); t != "" && t != "(untitled)" && t != "(untracked)" && t != "(new session)" {
		parts = append(parts, t)
	}
	parts = append(parts, statusWord(s))
	return truncate(compDesc(parts...), 100)
}

// teamValues lists the active teams and then the done ones.
func (d *compData) teamValues() []string {
	var active, done []store.Team
	for _, t := range d.teams {
		if t.Status == store.TeamActive {
			active = append(active, t)
		} else {
			done = append(done, t)
		}
	}
	out := make([]string, 0, len(d.teams))
	for _, t := range append(active, done...) {
		out = append(out, t.Name+"\t"+truncate(compText(t.Goal), 60))
	}
	return out
}

// roleValues lists a team's active roles with their tool and state.
func (d *compData) roleValues(tm store.Team) []string {
	var out []string
	for _, m := range d.members {
		if m.TeamID != tm.ID || !m.Active() {
			continue
		}
		out = append(out, m.Role+"\t"+compDesc(m.Tool, d.memberState(m)))
	}
	return out
}

// taskValues lists a board's tasks whose status is in statuses, in number
// order, with a one-line description.
func (d *compData) taskValues(tm store.Team, statuses map[string]bool) []string {
	var out []string
	for _, t := range d.tasks[tm.ID] {
		if statuses != nil && !statuses[t.Status] {
			continue
		}
		desc := compDesc(t.Title, t.Owner, t.Status)
		out = append(out, strconv.Itoa(t.N)+"\t"+desc)
	}
	return out
}

func (d *compData) lookupTeam(ref string) (store.Team, bool) {
	ref = strings.TrimSpace(strings.TrimPrefix(ref, "@"))
	if ref == "" {
		return store.Team{}, false
	}
	for _, t := range d.teams {
		if t.ID == ref || t.Name == ref {
			return t, true
		}
	}
	var found []store.Team
	for _, t := range d.teams {
		if strings.HasPrefix(t.ID, ref) || strings.HasPrefix(t.Name, ref) {
			found = append(found, t)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return store.Team{}, false
}

// defaultTeam is the team a command would work on without --team: the caller's
// team, or the only active one.
func (d *compData) defaultTeam() (store.Team, bool) {
	if ref := os.Getenv(team.TeamEnv); ref != "" {
		if t, ok := d.lookupTeam(ref); ok {
			return t, true
		}
	}
	if roles, err := newTracker(d.st).CallerRoles(); err == nil && len(roles) > 0 {
		return roles[0].Team, true
	}
	var active []store.Team
	for _, t := range d.teams {
		if t.Status == store.TeamActive {
			active = append(active, t)
		}
	}
	if len(active) == 1 {
		return active[0], true
	}
	return store.Team{}, false
}

// teamFor finds the team a command works on: its --team flag, else a named
// positional, else the caller's.
func (d *compData) teamFor(cmd *cobra.Command, args []string, named bool) (store.Team, bool) {
	if ref, err := cmd.Flags().GetString("team"); err == nil && ref != "" {
		return d.lookupTeam(ref)
	}
	if named && len(args) > 0 {
		if t, ok := d.lookupTeam(args[0]); ok {
			return t, true
		}
	}
	return d.defaultTeam()
}

// completeStore runs fn with the read-only data, and says nothing on any
// error.
func completeStore(fn func(d *compData) []string) ([]string, cobra.ShellCompDirective) {
	st, ok := openCompletionStore()
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer st.Close()
	return fn(loadCompData(st)), cobra.ShellCompDirectiveNoFileComp
}

func compSessions(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return completeStore(func(d *compData) []string { return d.sessionValues() })
}

func compTeams(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return completeStore(func(d *compData) []string { return d.teamValues() })
}

// compTeamThenRole completes a team, then a role of that team.
func compTeamThenRole(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	return completeStore(func(d *compData) []string {
		if len(args) == 0 {
			return d.teamValues()
		}
		if len(args) == 1 {
			if tm, ok := d.lookupTeam(args[0]); ok {
				return d.roleValues(tm)
			}
		}
		return nil
	})
}

// compTeamThenMember completes a team, then a member spec role=tool[:model].
func compTeamThenMember(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return compTeams(cmd, args, toComplete)
	}
	return compMemberSpec(cmd, args, toComplete)
}

// compTeamPositional completes a team in the first position and nothing after.
func compTeamPositional(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return compTeams(cmd, args, "")
}

// compRoles completes the caller's roles, or a --team's.
func compRoles(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return completeStore(func(d *compData) []string {
		tm, ok := d.teamFor(cmd, nil, false)
		if !ok {
			return nil
		}
		return d.roleValues(tm)
	})
}

// compSayRoles completes a role or "all" for `team say`.
func compSayRoles(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	values, dir := compRoles(cmd, args, "")
	values = append(values, store.ToAll+"\tevery member")
	return values, dir
}

// compLead completes who leads: a role, none, or you.
func compLead(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return compTeams(cmd, args, toComplete)
	}
	if len(args) > 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	values, dir := completeStore(func(d *compData) []string {
		tm, ok := d.lookupTeam(args[0])
		if !ok {
			return nil
		}
		return d.roleValues(tm)
	})
	values = append(values, "none\tleave it to you", "you\tlead from the tree")
	return values, dir
}

func compOwner(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return compRoles(cmd, nil, "")
}

func compAs(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return compRoles(cmd, nil, "")
}

// compTeamFlag completes a --team value.
func compTeamFlag(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return compTeams(cmd, nil, toComplete)
}

// taskStatuses are the statuses each board command can work on.
var taskStatuses = map[string]map[string]bool{
	"claim":  {store.TaskOpen: true, store.TaskClaimed: true, store.TaskBlocked: true},
	"done":   {store.TaskOpen: true, store.TaskClaimed: true, store.TaskBlocked: true},
	"block":  {store.TaskOpen: true, store.TaskClaimed: true, store.TaskBlocked: true},
	"edit":   {store.TaskOpen: true, store.TaskClaimed: true, store.TaskBlocked: true, store.TaskDone: true},
	"reopen": {store.TaskDone: true, store.TaskBlocked: true},
	"ls":     {store.TaskOpen: true, store.TaskClaimed: true, store.TaskBlocked: true, store.TaskDone: true},
}

func compTasks(verb string) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return completeStore(func(d *compData) []string {
			tm, ok := d.teamFor(cmd, nil, false)
			if !ok {
				return nil
			}
			return d.taskValues(tm, taskStatuses[verb])
		})
	}
}

// compAfter completes a comma-separated list of the tasks a task can wait for.
func compAfter(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	prefix, _ := afterPrefix(toComplete)
	return completeStore(func(d *compData) []string {
		tm, ok := d.teamFor(cmd, nil, false)
		if !ok {
			return nil
		}
		var out []string
		for _, t := range d.tasks[tm.ID] {
			if t.Status == store.TaskDone {
				continue
			}
			desc := compDesc(t.Title, t.Owner, t.Status)
			out = append(out, prefix+strconv.Itoa(t.N)+"\t"+desc)
		}
		return out
	})
}

// afterPrefix splits "1,2," into the part that stays and the part typed next.
func afterPrefix(s string) (string, string) {
	if i := strings.LastIndex(s, ","); i >= 0 {
		return s[:i+1], s[i+1:]
	}
	return "", s
}

// Tool and model completion.

func toolNames() []string {
	var out []string
	for _, a := range adapters.Current().Adapters {
		out = append(out, a.Spec().Name)
	}
	return out
}

func compTools(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	out := make([]string, 0, len(toolNames())+1)
	out = append(out, "auto\tpick the tool and model")
	for _, name := range toolNames() {
		out = append(out, name)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// compNewTool completes `hive new`'s tool.
func compNewTool(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	return compTools(cmd, nil, "")
}

// compModelOnNew completes --model from the tool already named.
func compModelOnNew(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	tool := ""
	for _, a := range args {
		if a == "auto" {
			tool = ""
			break
		}
		tool = a
		break
	}
	return completeStore(func(d *compData) []string {
		return d.modelValues(cmd.Context(), tool, "")
	})
}

// compLead completes --lead: a tool, a model after "tool:", or self/none.
func compLeadFlag(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if i := strings.Index(toComplete, ":"); i >= 0 {
		tool, prefix := toComplete[:i], toComplete[:i+1]
		return completeStore(func(d *compData) []string {
			return d.modelValues(cmd.Context(), tool, prefix)
		})
	}
	values, dir := compTools(cmd, nil, toComplete)
	values = append(values, "self\tlead as the agent running this", "none\tleave it to you")
	return values, dir
}

// compMemberSpec completes inside a member spec: tools after "role=" and
// models after "role=tool:".
func compMemberSpec(cmd *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	i := strings.LastIndex(toComplete, "=")
	if i < 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	prefix, rest := toComplete[:i+1], toComplete[i+1:]
	if j := strings.Index(rest, ":"); j >= 0 {
		tool, modelPrefix := rest[:j], prefix+rest[:j+1]
		return completeStore(func(d *compData) []string {
			return d.modelValues(cmd.Context(), tool, modelPrefix)
		})
	}
	values := make([]string, 0, len(toolNames())+1)
	values = append(values, prefix+"auto\tpick the tool and model")
	for _, name := range toolNames() {
		values = append(values, prefix+name)
	}
	return values, cobra.ShellCompDirectiveNoFileComp
}

// modelValues lists the models a tool can run, with prefix before each value
// (for "role=tool:" and "tool:"). An empty tool lists every tool's models.
func (d *compData) modelValues(ctx context.Context, tool, prefix string) []string {
	if ctx == nil {
		ctx = context.Background()
	}
	tr := newTracker(d.st)
	seen, _ := d.st.ModelsSeen()
	if tool != "" && tool != "auto" {
		out := make([]string, 0)
		for _, m := range d.toolModels(ctx, tr, tool, seen) {
			out = append(out, prefix+m+"\t"+tool)
		}
		return out
	}
	var out []string
	for _, name := range toolNames() {
		for _, m := range d.toolModels(ctx, tr, name, seen) {
			out = append(out, prefix+m+"\t"+name)
		}
	}
	return out
}

// toolModels lists a tool's models quickly. The tool's own listing gets two
// seconds; if it can't answer, its built-in and seen models are enough.
func (d *compData) toolModels(ctx context.Context, tr *tracker.Tracker, tool string, seen map[string][]string) []string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	models, err := tr.ToolModels(ctx, tool)
	if err == nil {
		return models
	}
	out := append([]string{}, seen[tool]...)
	for _, a := range adapters.Current().Adapters {
		if spec := a.Spec(); spec.Name == tool {
			out = append(out, spec.Models...)
		}
	}
	return out
}

// wireCompletion attaches the dynamic completions to the commands that take
// IDs, teams, roles and task numbers.
func wireCompletion(root *cobra.Command) {
	find := func(path ...string) *cobra.Command {
		c, _, err := root.Find(path)
		if err != nil {
			return nil
		}
		return c
	}
	sessions := []string{"send", "tail", "jump", "resume", "usage", "kill", "rm", "wait"}
	for _, name := range sessions {
		if c := find(name); c != nil {
			c.ValidArgsFunction = compSessions
		}
	}
	if c := find("trash", "restore"); c != nil {
		c.ValidArgsFunction = compSessions
	}
	if c := find("trash", "purge"); c != nil {
		c.ValidArgsFunction = compSessions
	}
	if c := find("team", "deliver"); c != nil {
		c.ValidArgsFunction = compSessions
	}

	for _, path := range [][]string{
		{"team", "board"}, {"team", "log"}, {"team", "done"},
		{"team", "pause"}, {"team", "resume"}, {"team", "stop"},
	} {
		if c := find(path...); c != nil {
			c.ValidArgsFunction = compTeamPositional
		}
	}
	for _, path := range [][]string{
		{"team", "rm"}, {"team", "mute"}, {"team", "unmute"},
	} {
		if c := find(path...); c != nil {
			c.ValidArgsFunction = compTeamThenRole
		}
	}
	if c := find("team", "add"); c != nil {
		c.ValidArgsFunction = compTeamThenMember
	}
	if c := find("team", "lead"); c != nil {
		c.ValidArgsFunction = compLead
	}
	if c := find("team", "say"); c != nil {
		c.ValidArgsFunction = compSayRoles
	}
	if c := find("team", "new"); c != nil {
		_ = c.RegisterFlagCompletionFunc("lead", compLeadFlag)
		_ = c.RegisterFlagCompletionFunc("member", compMemberSpec)
	}
	if c := find("new"); c != nil {
		c.ValidArgsFunction = compNewTool
		_ = c.RegisterFlagCompletionFunc("model", compModelOnNew)
		_ = c.RegisterFlagCompletionFunc("parent", compSessions)
	}

	for _, path := range [][]string{
		{"team", "task", "claim"}, {"team", "task", "done"},
		{"team", "task", "block"}, {"team", "task", "edit"},
		{"team", "task", "reopen"}, {"team", "task", "ls"},
	} {
		if c := find(path...); c != nil {
			c.ValidArgsFunction = compTasks(path[len(path)-1])
		}
	}
	for _, path := range [][]string{
		{"team", "task", "add"}, {"team", "task", "edit"},
	} {
		if c := find(path...); c != nil {
			_ = c.RegisterFlagCompletionFunc("after", compAfter)
			_ = c.RegisterFlagCompletionFunc("owner", compOwner)
		}
	}

	// Every command with a team, owner or role flag completes it the same way.
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if f := c.Flags().Lookup("team"); f != nil {
			_ = c.RegisterFlagCompletionFunc("team", compTeamFlag)
		}
		if f := c.Flags().Lookup("as"); f != nil {
			_ = c.RegisterFlagCompletionFunc("as", compAs)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
