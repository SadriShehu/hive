package tracker

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/spawn"
	"github.com/sadrishehu/hive/internal/store"
)

// How far after a spawning command its session may start. A titled spawn can
// wait long (a permission prompt, a queue); an untitled one is matched by
// time and folder alone, so the window stays short.
const (
	titledWindow   = 30 * time.Minute
	untitledWindow = 2 * time.Minute
	earlySlack     = 5 * time.Second // clocks and log timestamps disagree a little
	giveUpAfter    = 24 * time.Hour
)

// SyncResult summarizes one sync.
type SyncResult struct {
	Imported map[string]int // sessions read, per tool
	Linked   int            // past spawns newly linked to their parent
}

// Sync imports sessions from every tool's own storage and links past spawns:
// shell commands in any session's history that started another agent.
func (t *Tracker) Sync(ctx context.Context) (SyncResult, error) {
	var specs []agent.Spec
	for _, a := range t.Adapters {
		specs = append(specs, a.Spec())
	}
	res := SyncResult{Imported: map[string]int{}}
	purged, err := t.Store.PurgedIDs()
	if err != nil {
		return res, err
	}
	var errs []error
	for _, a := range t.Adapters {
		imp, ok := a.(agent.Importer)
		if !ok {
			continue
		}
		var hintErr error
		n, err := imp.Import(ctx, t.Store, func(c agent.ShellCommand) {
			if purged[c.SessionID] {
				return
			}
			for _, f := range spawn.Find(c.Command, c.Cwd, specs) {
				h := store.Hint{ParentID: c.SessionID, At: c.At, Tool: f.Tool, Title: f.Title,
					NativeID: f.NativeID, Cwd: f.Cwd, Headless: f.Headless}
				if err := t.Store.PutHint(h); err != nil && hintErr == nil {
					hintErr = err
				}
			}
		})
		res.Imported[a.Spec().Name] = n
		if err = errors.Join(err, hintErr); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.Spec().Name, err))
		}
	}
	linked, err := t.link()
	res.Linked = linked
	return res, errors.Join(append(errs, err)...)
}

// link matches open hints to the sessions they started. Commands that
// create a session go first, the most precise before the loosest: by title,
// then by time and folder. Commands naming a session by ID come last: they
// usually continue a session someone else started, so they only link
// sessions nothing else claims.
func (t *Tracker) link() (int, error) {
	hints, err := t.Store.OpenHints()
	if err != nil {
		return 0, err
	}
	order := func(h store.Hint) int {
		switch {
		case h.NativeID != "":
			return 2
		case h.Title != "":
			return 0
		}
		return 1
	}
	slices.SortStableFunc(hints, func(a, b store.Hint) int { return cmp.Compare(order(a), order(b)) })

	stale := t.World.Now() - giveUpAfter.Milliseconds()
	linked := 0
	for _, h := range hints {
		children, err := t.match(h)
		if err != nil {
			return linked, err
		}
		if len(children) == 0 {
			if h.At < stale {
				if err := t.Store.CloseHint(h, store.HintGaveUp); err != nil {
					return linked, err
				}
			}
			continue
		}
		for _, child := range children {
			if child == h.ParentID || t.cycles(child, h.ParentID) {
				continue
			}
			ok, err := t.Store.SetParent(child, h.ParentID)
			if err != nil {
				return linked, err
			}
			if ok {
				t.logf("link %s → %s (title %q)", h.ParentID, child, h.Title)
				linked++
			}
			if h.Headless && h.NativeID == "" {
				if err := t.Store.SetKindIfUnknown(child, store.KindHeadless); err != nil {
					return linked, err
				}
			}
		}
		if err := t.Store.CloseHint(h, children[0]); err != nil {
			return linked, err
		}
	}
	return linked, nil
}

// match finds the sessions a hint started: the one it names; else the first
// session of the tool to start soon after with the same title (every one
// matching, when the title came from a shell loop variable); else, without a
// title, the first in a related folder.
func (t *Tracker) match(h store.Hint) ([]string, error) {
	if h.NativeID != "" {
		id := store.ID(h.Tool, h.NativeID)
		_, ok, err := t.Store.Get(id)
		if !ok {
			return nil, err
		}
		return []string{id}, err
	}
	window := untitledWindow
	if h.Title != "" {
		window = titledWindow
	}
	candidates, err := t.Store.SpawnCandidates(h.Tool, h.ParentID,
		h.At-earlySlack.Milliseconds(), h.At+window.Milliseconds())
	if err != nil {
		return nil, err
	}
	if pattern := titlePattern(h.Title); pattern != nil {
		var ids []string
		for _, c := range candidates {
			if pattern.MatchString(c.Title) {
				ids = append(ids, c.ID)
			}
		}
		return ids, nil
	}
	for _, c := range candidates {
		if h.Title != "" && c.Title == h.Title || h.Title == "" && related(c.Cwd, h.Cwd) {
			return []string{c.ID}, nil
		}
	}
	return nil, nil
}

// shellExpansion matches what a shell substitutes: $var, ${var}, $(cmd), `cmd`.
var shellExpansion = regexp.MustCompile(`\$\{[^}]*\}|\$\([^)]*\)|\$[A-Za-z_][A-Za-z0-9_]*|\$[0-9@*#?]|` + "`[^`]*`")

// titlePattern turns a title containing shell expansions into a pattern
// matching whatever they expanded to; nil for a plain title.
func titlePattern(title string) *regexp.Regexp {
	if !shellExpansion.MatchString(title) {
		return nil
	}
	var b strings.Builder
	b.WriteString("^")
	last := 0
	for _, m := range shellExpansion.FindAllStringIndex(title, -1) {
		b.WriteString(regexp.QuoteMeta(title[last:m[0]]))
		b.WriteString(".+")
		last = m[1]
	}
	b.WriteString(regexp.QuoteMeta(title[last:]))
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// related reports whether two folders are the same or one contains the other.
func related(a, b string) bool {
	if a == "" || b == "" {
		return true
	}
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
