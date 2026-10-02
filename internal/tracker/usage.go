package tracker

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

const (
	usageBudget  = 5 * time.Second
	usageVersion = 1
)

func (t *Tracker) RefreshUsage(ctx context.Context, budget time.Duration) (read, pending int, err error) {
	sessions, err := t.Store.All()
	if err != nil {
		return 0, 0, err
	}
	records, err := t.Store.UsageRecords()
	if err != nil {
		return 0, 0, err
	}
	slices.SortFunc(sessions, usageOrder)
	start := time.Now()
	var errs []error
	for _, s := range sessions {
		reader, ok := t.usageReader(s)
		if !ok {
			continue
		}
		if err := ctx.Err(); err != nil {
			return read, pending, err
		}
		if budget > 0 && time.Since(start) > budget {
			pending++
			continue
		}
		changed, err := t.refreshOne(ctx, s, reader, records[s.ID])
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.ID, err))
			continue
		}
		if changed {
			read++
		}
	}
	return read, pending, errors.Join(errs...)
}

func (t *Tracker) Usage(ctx context.Context, s store.Session) (store.Usage, error) {
	rec, _, err := t.Store.UsageRecord(s.ID)
	if err != nil {
		return store.Usage{}, err
	}
	if reader, ok := t.usageReader(s); ok {
		if _, err := t.refreshOne(ctx, s, reader, rec); err != nil {
			return store.Usage{}, err
		}
	}
	u, ok, err := t.Store.Usage(s.ID)
	if err != nil || !ok {
		return store.Usage{ID: s.ID}, err
	}
	return t.Models.Apply(u), nil
}

func (t *Tracker) AllUsage() (map[string]store.Usage, error) {
	all, err := t.Store.AllUsage()
	if err != nil {
		return nil, err
	}
	for id, u := range all {
		all[id] = t.Models.Apply(u)
	}
	return all, nil
}

func (t *Tracker) usageReader(s store.Session) (agent.UsageReader, bool) {
	if s.Synthetic() {
		return nil, false
	}
	r, ok := t.adapter(s.Tool).(agent.UsageReader)
	return r, ok
}

func (t *Tracker) refreshOne(ctx context.Context, s store.Session, reader agent.UsageReader, rec store.UsageRecord) (bool, error) {
	prev, cursor := rec.Usage, rec.Cursor
	if rec.Version != usageVersion {
		prev, cursor = store.Usage{}, ""
	}
	u, next, err := reader.ReadUsage(ctx, s, prev, cursor)
	if errors.Is(err, agent.ErrNoUsage) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if next == cursor && rec.Version == usageVersion {
		return false, nil
	}
	u.ID = s.ID
	u.UpdatedAt = t.World.Now()
	return true, t.Store.UpsertUsage(store.UsageRecord{Usage: u, Cursor: next, Version: usageVersion})
}

func usageOrder(a, b store.Session) int {
	return cmp.Or(
		cmp.Compare(liveRank(a), liveRank(b)),
		cmp.Compare(b.UpdatedAt, a.UpdatedAt),
		cmp.Compare(a.ID, b.ID),
	)
}

func liveRank(s store.Session) int {
	if s.Live() {
		return 0
	}
	return 1
}
