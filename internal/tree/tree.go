// Package tree arranges sessions into spawn trees.
package tree

import (
	"cmp"
	"slices"

	"github.com/sadrishehu/hive/internal/store"
)

// Node is a session and the sessions it spawned.
type Node struct {
	store.Session
	Children []*Node
	Last     int64 // most recent activity anywhere in the subtree, epoch ms
	Live     bool  // something in the subtree is running
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
