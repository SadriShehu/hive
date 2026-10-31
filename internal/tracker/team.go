package tracker

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
	"github.com/sadrishehu/hive/internal/tmux"
)

// settle is how long a delivery waits, after a member's turn ends, for its
// TUI to take input again.
const settle = time.Second

func (t *Tracker) paneExists(pane string) bool {
	if t.HasPane != nil {
		return t.HasPane(pane)
	}
	return tmux.PaneExists(pane)
}

func (t *Tracker) paste(pane, text string) error {
	if t.Type != nil {
		return t.Type(pane, text)
	}
	return tmux.Paste(pane, text)
}

func newTeamID() string {
	var b [4]byte
	rand.Read(b[:])
	return fmt.Sprintf("%x", b[:])
}

// TeamOptions describes a team to start.
type TeamOptions struct {
	Name      string
	Goal      string
	Cwd       string
	Lead      *team.Spec     // the agent that leads; nil when the user does
	SelfLead  *store.Session // the agent running `hive team new` leads, as the lead role; nothing starts for it
	Members   []team.Spec
	CreatedBy string // the session running `hive team new`, if an agent
	Session   string // tmux session for the windows; "" means the home session
	Worktrees bool   // a git worktree and branch per member; the lead works in Cwd and merges
}

// MemberLaunch is the result of starting one member.
type MemberLaunch struct {
	Member   store.Member
	Launched Launched
	Err      error
}

// NewTeam records a team and starts its members, the lead first. A member
// that fails to start stays on the team with the error in its result. When
// none starts, the team isn't kept, and the error says why each failed.
func (t *Tracker) NewTeam(ctx context.Context, o TeamOptions) (store.Team, []MemberLaunch, error) {
	if err := team.ValidName(o.Name); err != nil {
		return store.Team{}, nil, err
	}
	specs := slices.Clone(o.Members)
	lead := ""
	if o.SelfLead != nil {
		lead = team.LeadRole
		specs = append([]team.Spec{{Role: lead, Tool: o.SelfLead.Tool}}, specs...)
	} else if o.Lead != nil {
		l := *o.Lead
		if l.Role == "" {
			l.Role = team.LeadRole
		}
		lead = l.Role
		specs = append([]team.Spec{l}, specs...)
	}
	if len(specs) == 0 {
		return store.Team{}, nil, errors.New("a team needs at least one member: --member role=tool, or --lead tool")
	}
	if err := team.CheckRoles(specs); err != nil {
		return store.Team{}, nil, err
	}
	cwd, err := folder(o.Cwd)
	if err != nil {
		return store.Team{}, nil, err
	}
	workspace, base := store.WorkspaceShared, store.TeamBase{}
	if o.Worktrees {
		if base, err = t.repoBase(o.Name, cwd); err != nil {
			return store.Team{}, nil, err
		}
		workspace = store.WorkspaceWorktrees
	}
	now := t.World.Now()
	tm := store.Team{ID: newTeamID(), Name: o.Name, Goal: strings.TrimSpace(o.Goal), Cwd: cwd,
		Workspace: workspace, CreatedBy: o.CreatedBy, Status: store.TeamActive,
		LeadRole: lead, CreatedAt: now, UpdatedAt: now}
	if err := t.Store.CreateTeam(tm); err != nil {
		return store.Team{}, nil, err
	}
	if o.Worktrees {
		if err := t.startWorktrees(tm, base); err != nil {
			return store.Team{}, nil, errors.Join(err, t.Store.ForgetWorktrees(tm.ID), t.Store.DeleteTeam(tm.ID))
		}
	}
	out, err := t.startTeam(ctx, tm, specs, o.SelfLead, o.Session)
	if err != nil {
		return store.Team{}, nil, errors.Join(err, t.dropWorktrees(tm), t.Store.DeleteTeam(tm.ID))
	}
	return tm, out, nil
}

// startTeam puts a new team's members on it and starts them. It fails when
// none starts.
func (t *Tracker) startTeam(ctx context.Context, tm store.Team, specs []team.Spec, self *store.Session, session string) ([]MemberLaunch, error) {
	for _, sp := range specs {
		m := store.Member{TeamID: tm.ID, Role: sp.Role, Tool: sp.Tool, Model: sp.Model, Prompt: sp.Prompt, JoinedAt: tm.CreatedAt}
		if self != nil && sp.Role == tm.LeadRole {
			m.SessionID = self.ID
		}
		if err := t.Store.AddMember(m); err != nil {
			return nil, err
		}
	}
	members, err := t.Store.Members(tm.ID)
	if err != nil {
		return nil, err
	}
	goal := tm.Goal
	if goal == "" {
		goal = "(no goal set)"
	}
	if _, err := t.system(tm, "team started: "+goal); err != nil {
		return nil, err
	}
	// Members picked with auto ask each tool what it runs once for the team.
	t.toolModels = map[string][]string{}
	defer func() { t.toolModels = nil }()
	var out []MemberLaunch
	var failed []string
	for _, m := range members {
		if m.SessionID != "" { // the agent that made the team: already running
			t.system(tm, m.Role+" is "+m.SessionID+", the agent that made the team")
			out = append(out, MemberLaunch{Member: m, Launched: Launched{SessionID: m.SessionID, Tool: m.Tool}})
			continue
		}
		l, err := t.startMember(ctx, tm, m, members, session)
		out = append(out, MemberLaunch{Member: m, Launched: l, Err: err})
		if err != nil && l.Pane == "" { // no window opened
			failed = append(failed, m.Role+": "+err.Error())
		}
	}
	if len(failed) == len(out) {
		return nil, errors.New("no member started, so the team wasn't kept: " + strings.Join(failed, "; "))
	}
	return out, nil
}

// startMember opens a member's window with its brief as the first prompt.
func (t *Tracker) startMember(ctx context.Context, tm store.Team, m store.Member, members []store.Member, session string) (Launched, error) {
	// auto is rated on the goal and the member's part, not on the brief.
	if (PickOptions{Tool: m.Tool, Model: m.Model}).Wants() {
		choice, err := t.Pick(ctx, PickOptions{Tool: m.Tool, Model: m.Model, Prompt: strings.TrimSpace(tm.Goal + "\n" + m.Prompt), ParentID: tm.CreatedBy})
		if err != nil {
			t.system(tm, m.Role+" didn't start: "+err.Error())
			return Launched{}, err
		}
		m.Tool, m.Model = choice.Tool, choice.ID
		if err := t.Store.SetMemberTool(tm.ID, m.Role, m.Tool, m.Model); err != nil {
			return Launched{}, err
		}
	}
	if err := t.placeMember(tm, &m); err != nil {
		t.system(tm, m.Role+" didn't start: "+err.Error())
		return Launched{}, err
	}
	l, err := t.Launch(ctx, LaunchOptions{Tool: m.Tool, Model: m.Model, Cwd: team.Folder(tm, m), Prompt: team.Brief(tm, m, members),
		ParentID: tm.CreatedBy, Session: session, Detached: true,
		Env: map[string]string{team.TeamEnv: tm.ID, team.RoleEnv: m.Role}})
	if err != nil {
		t.system(tm, m.Role+" didn't start: "+err.Error())
		return l, err
	}
	err = errors.Join(
		t.Store.SetMemberSession(tm.ID, m.Role, l.SessionID, l.Pane),
		t.Store.PutLaunchTeam(store.LaunchTeam{Pane: l.Pane, TeamID: tm.ID, Role: m.Role, CreatedAt: t.World.Now()}),
	)
	what := m.Tool
	if m.Model != "" {
		what += ":" + m.Model
	}
	_, perr := t.system(tm, m.Role+" started ("+what+")")
	return l, errors.Join(err, perr)
}

// AddMember puts one more member on a team and starts it.
func (t *Tracker) AddMember(ctx context.Context, tm store.Team, sp team.Spec, session string) (store.Member, Launched, error) {
	if err := team.ValidRole(sp.Role); err != nil {
		return store.Member{}, Launched{}, err
	}
	if err := closed(tm); err != nil {
		return store.Member{}, Launched{}, err
	}
	m := store.Member{TeamID: tm.ID, Role: sp.Role, Tool: sp.Tool, Model: sp.Model, Prompt: sp.Prompt, JoinedAt: t.World.Now()}
	if err := t.Store.AddMember(m); err != nil {
		return m, Launched{}, err
	}
	members, err := t.Store.Members(tm.ID)
	if err != nil {
		return m, Launched{}, err
	}
	l, err := t.startMember(ctx, tm, m, members, session)
	return m, l, err
}

// RetireMember takes a member off the team and stops it if it runs.
func (t *Tracker) RetireMember(tm store.Team, role string) error {
	members, err := t.Store.Members(tm.ID)
	if err != nil {
		return err
	}
	var stop *store.Session
	for _, m := range members {
		if m.Role == role && m.Active() && m.SessionID != "" {
			if s, ok, _ := t.Store.Get(m.SessionID); ok && s.Live() {
				stop = &s
			}
		}
	}
	if err := t.Store.RetireMember(tm.ID, role, t.World.Now()); err != nil {
		return err
	}
	if tm.LeadRole == role {
		if err := t.Store.SetLead(tm.ID, "", t.World.Now()); err != nil {
			return err
		}
	}
	if _, err := t.system(tm, role+" left the team"); err != nil {
		return err
	}
	if stop != nil {
		t.World.Forget()
		return t.Stop(*stop)
	}
	return nil
}

// StopTeam stops every member that runs and returns how many it stopped.
func (t *Tracker) StopTeam(tm store.Team) (int, error) {
	members, err := t.Store.Members(tm.ID)
	if err != nil {
		return 0, err
	}
	t.World.Forget()
	var errs []error
	n := 0
	for _, m := range members {
		if !m.Active() || m.SessionID == "" {
			continue
		}
		s, ok, err := t.Store.Get(m.SessionID)
		if err != nil || !ok || s.PID <= 0 {
			continue
		}
		if err := t.Stop(s); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.Role, err))
			continue
		}
		n++
	}
	if n > 0 {
		t.system(tm, fmt.Sprintf("stopped %d members", n))
	}
	return n, errors.Join(errs...)
}

// SetPaused pauses a team, or resumes it and delivers what waited.
func (t *Tracker) SetPaused(tm store.Team, paused bool) error {
	if err := t.Store.SetPaused(tm.ID, paused, t.World.Now()); err != nil {
		return err
	}
	tm.Paused = paused
	if paused {
		_, err := t.system(tm, "team paused: no mail is delivered, no tasks are claimed")
		return err
	}
	if _, err := t.system(tm, "team resumed"); err != nil {
		return err
	}
	members, err := t.Store.Members(tm.ID)
	if err != nil {
		return err
	}
	var errs []error
	for _, m := range members {
		if m.Active() {
			_, err := t.deliverTo(tm, m)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// SetLead hands the lead to a member, or to the user with role "".
func (t *Tracker) SetLead(tm store.Team, role string) error {
	if role != "" {
		members, err := t.Store.Members(tm.ID)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(members, func(m store.Member) bool { return m.Role == role && m.Active() }) {
			return fmt.Errorf("the team has no %s; roles: %s", role, team.Roster(members))
		}
	}
	if err := t.Store.SetLead(tm.ID, role, t.World.Now()); err != nil {
		return err
	}
	text := "the user leads now"
	if role != "" {
		text = role + " leads now"
	}
	_, err := t.system(tm, text)
	return err
}

// system adds hive's own post to the channel.
func (t *Tracker) system(tm store.Team, body string) (store.Message, error) {
	return t.Store.AddMessage(store.Message{TeamID: tm.ID, From: store.FromHive, Body: body,
		Kind: store.MsgSystem, At: t.World.Now()}, nil)
}

// Role is a member with its team.
type Role struct {
	Team   store.Team
	Member store.Member
}

// roles returns the roles session id plays, newest first: one on each team
// it is on.
func (t *Tracker) roles(id string) ([]Role, error) {
	ms, err := t.Store.Memberships(id)
	if err != nil {
		return nil, err
	}
	var out []Role
	for _, m := range ms {
		tm, err := t.Store.TeamByID(m.TeamID)
		if err != nil {
			return nil, err
		}
		out = append(out, Role{Team: tm, Member: m})
	}
	return out, nil
}

// teamNote posts "<role> <what>" for each role session id plays.
func (t *Tracker) teamNote(id, what string) {
	roles, _ := t.roles(id)
	for _, r := range roles {
		t.system(r.Team, r.Member.Role+" "+what)
	}
}

// memberEnv is what a window opened for session id carries when the session
// plays a role: HIVE_TEAM and HIVE_ROLE for its newest one.
func (t *Tracker) memberEnv(id string) map[string]string {
	ms, err := t.Store.Memberships(id)
	if err != nil || len(ms) == 0 {
		return nil
	}
	return map[string]string{team.TeamEnv: ms[0].TeamID, team.RoleEnv: ms[0].Role}
}

// rememberMember records that the window just opened for a session plays its
// roles, so the session it reports under is linked to them.
func (t *Tracker) rememberMember(sessionID, pane string) error {
	ms, err := t.Store.Memberships(sessionID)
	if err != nil {
		return err
	}
	var errs []error
	for _, m := range ms {
		errs = append(errs,
			t.Store.PutLaunchTeam(store.LaunchTeam{Pane: pane, TeamID: m.TeamID, Role: m.Role, CreatedAt: t.World.Now()}),
			t.Store.SetMemberPane(m.TeamID, m.Role, pane))
	}
	return errors.Join(errs...)
}

// handOffRoles keeps the roles of session s, which just ended, for the next
// session its process starts in the same pane, as Claude does on /clear.
func (t *Tracker) handOffRoles(s store.Session, at int64) error {
	if s.PID <= 0 || s.Pane == "" {
		return nil
	}
	ms, err := t.Store.Memberships(s.ID)
	if err != nil {
		return err
	}
	for _, m := range ms {
		if err := t.Store.PutLaunchTeam(store.LaunchTeam{Pane: s.Pane, TeamID: m.TeamID, Role: m.Role,
			PID: s.PID, CreatedAt: at}); err != nil {
			return err
		}
	}
	return nil
}

// takeOverRoles hands the roles of the other sessions that ran as pid to id:
// the process went on to a new session (/clear in Claude, /new in opencode).
func (t *Tracker) takeOverRoles(pid int, id, pane string) error {
	prev, err := t.Store.OnPID(pid)
	if err != nil {
		return err
	}
	for _, s := range prev {
		if s.ID == id {
			continue
		}
		ms, err := t.Store.Memberships(s.ID)
		if err != nil {
			return err
		}
		for _, m := range ms {
			if err := t.moveRole(m.TeamID, m.Role, id, pane); err != nil {
				return err
			}
		}
	}
	return nil
}

// linkMember ties the session that reported in from pane, running as pid, to
// the roles hive opened that pane for, or that the process's last session
// played. A record that has expired, or whose process is gone, links nothing.
func (t *Tracker) linkMember(id string, pid int, pane string) error {
	if pane == "" {
		return nil
	}
	launches, err := t.Store.TakeLaunchTeams(pane, t.World.Now()-launchTTL.Milliseconds())
	if err != nil {
		return err
	}
	for _, l := range launches {
		if l.PID > 0 && l.PID != pid {
			continue
		}
		if err := t.moveRole(l.TeamID, l.Role, id, pane); err != nil {
			return err
		}
	}
	return nil
}

// moveRole makes session id play a role still on its team, and says so in
// the channel when another session played it.
func (t *Tracker) moveRole(teamID, role, id, pane string) error {
	members, err := t.Store.Members(teamID)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(members, func(m store.Member) bool { return m.Role == role && m.Active() })
	if i < 0 || members[i].SessionID == id {
		return nil
	}
	if err := t.Store.SetMemberSession(teamID, role, id, pane); err != nil {
		return err
	}
	if members[i].SessionID != "" {
		if tm, err := t.Store.TeamByID(teamID); err == nil {
			t.system(tm, role+" continues as "+id)
		}
	}
	return nil
}

// deliverable tells OnDeliverable that id's agent can take the mail queued
// for any role it plays on a team that isn't paused.
func (t *Tracker) deliverable(id string) {
	if t.OnDeliverable == nil {
		return
	}
	roles, err := t.roles(id)
	if err != nil {
		return
	}
	for _, r := range roles {
		if r.Team.Paused {
			continue
		}
		if pending, err := t.Store.HasPending(r.Team.ID, r.Member.Role); err == nil && pending {
			if s, ok, err := t.Store.Get(id); err == nil && ok {
				t.OnDeliverable(s)
			}
			return
		}
	}
}

// CallerRoles finds the roles of the team member running this process. The
// member is, in order: the nearest session up the process tree, or up the
// parent links from it, that plays a role; the one HIVE_TEAM and HIVE_ROLE
// name, which every member's window carries; the nearest up the parent
// links from the session the environment names. The process tree comes
// before HIVE_PARENT, which in a member's window names the agent that made
// the team.
func (t *Tracker) CallerRoles() ([]Role, error) {
	if roles, err := t.rolesUp(t.ancestorSession("", t.World.SelfPID())); err != nil || len(roles) > 0 {
		return roles, err
	}
	if roles, err := t.envRoles(); err != nil || len(roles) > 0 {
		return roles, err
	}
	id, _ := t.envSession("")
	return t.rolesUp(id)
}

// rolesUp returns the roles of the nearest session that plays any, from id
// up its parent links: a member's subagent or child speaks for the member.
func (t *Tracker) rolesUp(id string) ([]Role, error) {
	for range 16 {
		if id == "" {
			return nil, nil
		}
		roles, err := t.roles(id)
		if err != nil || len(roles) > 0 {
			return roles, err
		}
		s, ok, err := t.Store.Get(id)
		if err != nil || !ok {
			return nil, err
		}
		id = s.ParentID
	}
	return nil, nil
}

// envRoles returns the role HIVE_TEAM and HIVE_ROLE name, with every other
// role its session plays.
func (t *Tracker) envRoles() ([]Role, error) {
	teamID, role := t.World.Getenv(team.TeamEnv), t.World.Getenv(team.RoleEnv)
	if teamID == "" || role == "" {
		return nil, nil
	}
	tm, err := t.Store.TeamByID(teamID)
	if err != nil {
		return nil, nil // the team is gone
	}
	members, err := t.Store.Members(tm.ID)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(members, func(m store.Member) bool { return m.Role == role && m.Active() })
	if i < 0 {
		return nil, nil
	}
	if roles, err := t.roles(members[i].SessionID); err != nil || len(roles) > 0 {
		return roles, err
	}
	return []Role{{Team: tm, Member: members[i]}}, nil
}

// Sent says what happened to a message for one recipient.
type Sent struct {
	Role string
	How  string
}

// Say writes to a teammate or to everyone, as from, and tries to deliver it.
func (t *Tracker) Say(tm store.Team, from, to, body string, private bool) (store.Message, []Sent, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return store.Message{}, nil, errors.New("nothing to say")
	}
	if err := closed(tm); err != nil {
		return store.Message{}, nil, err
	}
	members, err := t.Store.Members(tm.ID)
	if err != nil {
		return store.Message{}, nil, err
	}
	to = strings.TrimPrefix(to, "@")
	roles, err := team.Recipients(to, from, members)
	if err != nil {
		return store.Message{}, nil, err
	}
	if len(roles) == 0 {
		return store.Message{}, nil, errors.New("there is nobody else on the team to tell")
	}
	msg, err := t.Store.AddMessage(store.Message{TeamID: tm.ID, From: from, To: to, Body: body,
		Kind: store.MsgSay, Private: private, At: t.World.Now()}, roles)
	if err != nil {
		return msg, nil, err
	}
	return msg, t.deliverAll(tm, members, roles), nil
}

// Post writes to the channel as from. It reaches a pane only when the body
// names a teammate with @role.
func (t *Tracker) Post(tm store.Team, from, body string) (store.Message, []Sent, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return store.Message{}, nil, errors.New("nothing to post")
	}
	if err := closed(tm); err != nil {
		return store.Message{}, nil, err
	}
	members, err := t.Store.Members(tm.ID)
	if err != nil {
		return store.Message{}, nil, err
	}
	var roles []string
	for _, r := range team.Mentions(body, members) {
		if r != from {
			roles = append(roles, r)
		}
	}
	msg, err := t.Store.AddMessage(store.Message{TeamID: tm.ID, From: from, Body: body,
		Kind: store.MsgPost, At: t.World.Now()}, roles)
	if err != nil {
		return msg, nil, err
	}
	return msg, t.deliverAll(tm, members, roles), nil
}

func (t *Tracker) deliverAll(tm store.Team, members []store.Member, roles []string) []Sent {
	var out []Sent
	for _, r := range roles {
		i := slices.IndexFunc(members, func(m store.Member) bool { return m.Role == r })
		how, err := t.deliverTo(tm, members[i])
		if err != nil {
			how = "queued; " + err.Error()
		}
		out = append(out, Sent{Role: r, How: how})
	}
	return out
}

// deliverTo types a member's queued messages into its pane, or reopens it
// with them, when the rules allow; otherwise they wait. It says which.
func (t *Tracker) deliverTo(tm store.Team, m store.Member) (string, error) {
	return t.deliver(m.SessionID, []Role{{Team: tm, Member: m}})
}

// deliver types what is queued for the roles session id plays into its pane,
// as one paste, or reopens it with them, when the rules allow; otherwise the
// messages wait. It says which.
func (t *Tracker) deliver(id string, roles []Role) (string, error) {
	var ready []Role
	why := "nothing waits"
	for _, r := range roles {
		pending, err := t.Store.HasPending(r.Team.ID, r.Member.Role)
		if err != nil {
			return "", err
		}
		muted, err := t.Store.Muted(r.Team.ID, r.Member.Role)
		if err != nil {
			return "", err
		}
		switch {
		case !pending:
		case !r.Member.Active():
			why = "queued; it left the team"
		case r.Team.Paused:
			why = "queued; the team is paused"
		case muted:
			why = "queued; it is muted"
		default:
			ready = append(ready, r)
		}
	}
	if len(ready) == 0 {
		return why, nil
	}
	s, ok, err := t.Store.Get(id)
	if err != nil {
		return "", err
	}
	if id == "" || !ok {
		return "queued; it hasn't reported in yet", nil
	}
	status := s.Status
	if !s.Live() {
		status = store.StatusExited
	}
	d := team.Decide(false, status, s.Pane != "" && t.paneExists(s.Pane)) // paused teams are out already
	if d == team.Queue {
		if status == store.StatusAttention {
			return "queued; it is waiting on you", nil
		}
		return "queued; it is " + status, nil
	}
	type claim struct {
		team store.Team
		role string
		msgs []store.Message
	}
	var claims []claim
	unclaim := func() error {
		var errs []error
		for _, c := range claims {
			errs = append(errs, t.Store.Unclaim(c.role, c.msgs))
		}
		return errors.Join(errs...)
	}
	var text []string
	for _, r := range ready {
		msgs, err := t.Store.ClaimPending(r.Team.ID, r.Member.Role, t.World.Now())
		if err != nil {
			return "", errors.Join(err, unclaim())
		}
		if len(msgs) > 0 {
			claims = append(claims, claim{r.Team, r.Member.Role, msgs})
			text = append(text, team.Paste(r.Team.Name, msgs))
		}
	}
	if len(claims) == 0 {
		return "nothing waits", nil
	}
	how, err := t.Send(s, strings.Join(text, "\n"), LaunchOptions{Detached: true})
	if err != nil {
		return "", errors.Join(err, unclaim())
	}
	if d == team.Blind {
		how += " (blind: it doesn't report its turns)"
	}
	if d == team.Reopen { // no hook says it started again
		for _, c := range claims {
			t.system(c.team, c.role+" reopened with its mail")
		}
	}
	return how, nil
}

// Deliver types the mail queued for the roles session id plays. It is what
// `hive team deliver` runs after a member's turn ends: it lets the TUI
// settle, looks again at the member, its teams and its status, and then
// types everything as one paste.
func (t *Tracker) Deliver(id string, wait time.Duration) (string, error) {
	roles, err := t.roles(id)
	if err != nil || len(roles) == 0 {
		return "not a team member", err
	}
	if wait > 0 {
		time.Sleep(wait)
		t.World.Forget()
		// A team may have been paused meanwhile, or the member retired.
		if roles, err = t.roles(id); err != nil || len(roles) == 0 {
			return "not a team member", err
		}
		if s, ok, err := t.Store.Get(id); err != nil || !ok || s.Status != store.StatusIdle {
			return "it is busy again; the next idle delivers", err
		}
	}
	return t.deliver(id, roles)
}

// Settle is the pause Deliver takes after an idle event.
func Settle() time.Duration { return settle }

// Inbox returns the messages queued for a member and marks them delivered.
// A paused team keeps them.
func (t *Tracker) Inbox(teamID, role string) ([]store.Message, error) {
	tm, err := t.Store.TeamByID(teamID)
	if err != nil || tm.Paused {
		return nil, err
	}
	return t.Store.ClaimPending(tm.ID, role, t.World.Now())
}

// MuteMember stops hive from typing a member's queued mail into its pane, or
// lets it again. The mail still queues, and `hive team inbox` still reads it.
func (t *Tracker) MuteMember(tm store.Team, role string, on bool) error {
	role = strings.TrimPrefix(strings.TrimSpace(role), "@")
	members, err := t.Store.Members(tm.ID)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(members, func(m store.Member) bool { return m.Active() && m.Role == role }) {
		return fmt.Errorf("the team has no %s; roles: %s", role, team.Roster(members))
	}
	if on {
		return t.Store.Mute(tm.ID, role, t.World.Now())
	}
	return t.Store.Unmute(tm.ID, role)
}

// MemberState says what a member is doing: its session's status, or that it
// hasn't started, or ended.
func MemberState(m store.Member, s store.Session, found bool) string {
	switch {
	case !m.Active():
		return "left"
	case m.SessionID == "" || !found:
		return "starting"
	case !s.Live() && s.Status != store.StatusUnknown:
		return "ended"
	}
	if s.Status == store.StatusAttention {
		return "needs you"
	}
	return s.Status
}
