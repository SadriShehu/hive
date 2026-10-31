// Package tree arranges sessions into spawn trees.
package tree

import (
	"cmp"
	"slices"
	"strings"

	"github.com/sadrishehu/hive/internal/store"
)

// Node is a session and the sessions it spawned.
type Node struct {
	store.Session
	Children []*Node
	Last     int64 // most recent activity anywhere in the subtree, epoch ms
	Live     bool  // something in the subtree is running

	Team   *store.Team // set on a team's node, whose Session is a stand-in for it
	TeamID string      // set on a team's node and on its members
	Role   string      // set on a member: the role its session plays
}

// TeamPrefix starts the ID of the stand-in session of a team's node.
const TeamPrefix = "team:"

// BuildTeams is Build with the teams in it. A team is a node of its own, at
// the root or under the session that made it, with its members as its
// children; a member's own sessions stay under the member. Members the tree
// can't find a session for yet are left out.
func BuildTeams(sessions []store.Session, teams []store.Team, members []store.Member) []*Node {
	if len(teams) == 0 {
		return Build(sessions)
	}
	byID := make(map[string]int, len(sessions))
	for i, s := range sessions {
		byID[s.ID] = i
	}
	all := append([]store.Session(nil), sessions...)
	roles := map[string]store.Member{} // by the session that plays the role
	onTeam := map[[2]string]bool{}     // session and team: a session can be on several
	for _, m := range members {
		if !m.Active() {
			continue
		}
		i, ok := byID[m.SessionID]
		if !ok && m.Pane != "" { // not reported in yet: the pending session in its pane
			for j, s := range sessions {
				if s.Pane == m.Pane && s.Synthetic() {
					i, ok = j, true
				}
			}
		}
		if ok {
			roles[all[i].ID] = m
			onTeam[[2]string{all[i].ID, m.TeamID}] = true
		}
	}
	byTeam := map[string]store.Team{}
	standIns := map[string]*store.Session{}
	for _, t := range teams {
		byTeam[t.ID] = t
		st := store.Session{ID: TeamPrefix + t.ID, Tool: "team", Title: t.Name, Cwd: t.Cwd, Kind: "team",
			Status: store.StatusUnknown, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, Source: store.SourceTeam}
		if _, ok := byID[t.CreatedBy]; ok && t.CreatedBy != "" && !onTeam[[2]string{t.CreatedBy, t.ID}] {
			st.ParentID = t.CreatedBy
		}
		all = append(all, st)
	}
	// Appending may have moved the slice: take the stand-ins again.
	for i := range all {
		if all[i].Source == store.SourceTeam {
			standIns[strings.TrimPrefix(all[i].ID, TeamPrefix)] = &all[i]
		}
	}
	for i := range all {
		m, ok := roles[all[i].ID]
		if !ok {
			continue
		}
		if t, ok := standIns[m.TeamID]; ok {
			all[i].ParentID = t.ID
			if rank(all[i].Status) > rank(t.Status) && all[i].Live() {
				t.Status = all[i].Status
			}
			t.UpdatedAt = max(t.UpdatedAt, all[i].UpdatedAt)
		}
	}
	roots := Build(all)
	var annotate func(nodes []*Node)
	annotate = func(nodes []*Node) {
		for _, n := range nodes {
			switch {
			case n.Source == store.SourceTeam:
				t := byTeam[strings.TrimPrefix(n.ID, TeamPrefix)]
				n.Team, n.TeamID = &t, t.ID
			case roles[n.ID].Role != "":
				n.TeamID, n.Role = roles[n.ID].TeamID, roles[n.ID].Role
			}
			annotate(n.Children)
		}
	}
	annotate(roots)
	return roots
}

// rank orders statuses by how much they ask of the user.
func rank(status string) int {
	switch status {
	case store.StatusAttention:
		return 4
	case store.StatusWorking:
		return 3
	case store.StatusIdle:
		return 2
	case store.StatusExited:
		return 1
	}
	return 0
}

// Build links sessions to their parents. A session whose parent is unknown
// is a root. Trees with something running come first, then the most recently
// active; children are in the order they started.
func Build(sessions []store.Session) []*Node {
	nodes := make(map[string]*Node, len(sessions))
	for _, s := range sessions {
		nodes[s.ID] = &Node{Session: s}
	}
	var roots []*Node
	for _, s := range sessions {
		n := nodes[s.ID]
		if p, ok := nodes[s.ParentID]; ok && s.ParentID != s.ID && !reaches(nodes, p, s.ID) {
			p.Children = append(p.Children, n)
		} else {
			roots = append(roots, n)
		}
	}
	for _, r := range roots {
		summarize(r)
	}
	slices.SortFunc(roots, func(a, b *Node) int {
		if a.Live != b.Live {
			if a.Live {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(b.Last, a.Last), cmp.Compare(a.ID, b.ID))
	})
	return roots
}

// reaches reports whether id is an ancestor of n, which would make a cycle.
func reaches(nodes map[string]*Node, n *Node, id string) bool {
	for range 64 {
		if n.ID == id {
			return true
		}
		p, ok := nodes[n.ParentID]
		if !ok || n.ParentID == n.ID {
			return false
		}
		n = p
	}
	return true
}

func summarize(n *Node) {
	n.Last = n.UpdatedAt
	n.Live = n.Session.Live()
	for _, c := range n.Children {
		summarize(c)
		n.Last = max(n.Last, c.Last)
		n.Live = n.Live || c.Live
	}
	slices.SortFunc(n.Children, func(a, b *Node) int {
		return cmp.Or(cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
}

// Filter keeps nodes for which keep is true, plus the ancestors that lead to them.
func Filter(nodes []*Node, keep func(*Node) bool) []*Node {
	var out []*Node
	for _, n := range nodes {
		kids := Filter(n.Children, keep)
		if keep(n) || len(kids) > 0 {
			c := *n
			c.Children = kids
			out = append(out, &c)
		}
	}
	return out
}

// FilterTeams keeps the team nodes for which keep is true, with their whole
// subtree, plus the ancestors that lead to them. A team's members' children
// don't carry the TeamID, so Filter would drop a member's subagents; this
// keeps everything under a matching team however deep it goes.
func FilterTeams(nodes []*Node, keep func(*Node) bool) []*Node {
	var visit func(nodes []*Node, within bool) []*Node
	visit = func(nodes []*Node, within bool) []*Node {
		var out []*Node
		for _, n := range nodes {
			mine := within || (n.Team != nil && keep(n))
			c := *n
			c.Children = visit(n.Children, mine)
			if mine || len(c.Children) > 0 {
				out = append(out, &c)
			}
		}
		return out
	}
	return visit(nodes, false)
}

// Walk visits nodes depth-first, passing the box-drawing prefix that places
// each one in the tree ("├─ ", "│  └─ ", …).
func Walk(roots []*Node, fn func(n *Node, prefix string)) {
	walk(roots, "", true, fn)
}

func walk(nodes []*Node, indent string, root bool, fn func(*Node, string)) {
	for i, n := range nodes {
		prefix, next := "", ""
		if !root {
			if i == len(nodes)-1 {
				prefix, next = indent+"└─ ", indent+"   "
			} else {
				prefix, next = indent+"├─ ", indent+"│  "
			}
		}
		fn(n, prefix)
		walk(n.Children, next, false, fn)
	}
}

// Recent keeps the trees with something running or activity since cutoff.
func Recent(roots []*Node, cutoff int64) []*Node {
	var out []*Node
	for _, r := range roots {
		if r.Live || r.Last >= cutoff {
			out = append(out, r)
		}
	}
	return out
}

// DropEmpty removes sessions that ended with nothing in them: no title, no
// prompt, and nothing left under them once their own empty children are
// gone. Agents pre-warm spare sessions that never get used.
func DropEmpty(nodes []*Node) []*Node {
	var out []*Node
	for _, n := range nodes {
		c := *n
		c.Children = DropEmpty(n.Children)
		if !c.Live && c.Title == "" && c.LastPrompt == "" && len(c.Children) == 0 && !c.Synthetic() {
			continue
		}
		out = append(out, &c)
	}
	return out
}

// Prune removes the nodes for which drop is true, with everything under them.
func Prune(nodes []*Node, drop func(*Node) bool) []*Node {
	var out []*Node
	for _, n := range nodes {
		if drop(n) {
			continue
		}
		c := *n
		c.Children = Prune(n.Children, drop)
		out = append(out, &c)
	}
	return out
}

func Cost(nodes []*Node, usages map[string]store.Usage) (total float64, estimate bool) {
	for _, n := range nodes {
		u, ok := usages[n.Session.ID]
		if ok && u.CostSource != "" {
			total += u.CostUSD
			estimate = estimate || u.CostSource == store.CostConfig
		}
		children := n.Children
		if ok && u.CostCoversChildren() {
			children = externalChildren(n)
		}
		sub, subEstimate := Cost(children, usages)
		total += sub
		estimate = estimate || subEstimate
	}
	return total, estimate
}

func externalChildren(n *Node) []*Node {
	var out []*Node
	for _, c := range n.Children {
		if c.Session.Kind != store.KindInternal {
			out = append(out, c)
		}
	}
	return out
}
