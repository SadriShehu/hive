package opencode

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"strconv"
	"time"

	"github.com/tidwall/gjson"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

func (a *Adapter) ReadUsage(ctx context.Context, s store.Session, prev store.Usage, cursor string) (store.Usage, string, error) {
	db, err := a.openDB(2 * time.Second)
	if errors.Is(err, fs.ErrNotExist) {
		return prev, cursor, agent.ErrNoUsage
	}
	if err != nil {
		return prev, cursor, err
	}
	defer db.Close()
	layout, err := layoutOf(ctx, db)
	if err != nil {
		return prev, cursor, err
	}
	var updated int64
	err = db.QueryRowContext(ctx, layout.sessionUpdatedAt, s.NativeID).Scan(&updated)
	if errors.Is(err, sql.ErrNoRows) {
		return prev, cursor, agent.ErrNoUsage
	}
	if err != nil {
		return prev, cursor, err
	}
	next := strconv.FormatInt(updated, 10)
	if next == cursor {
		return prev, cursor, nil
	}
	u := store.Usage{ID: s.ID, CostSource: store.CostTool, Models: map[string]store.Tokens{}, Tools: map[string]int{}, Skills: map[string]int{}}
	for _, read := range []func(context.Context, *sql.DB, databaseLayout, string, *store.Usage) error{readModels, readLastMessage, readTools, readSkills, readCompactions} {
		if err := read(ctx, db, layout, s.NativeID, &u); err != nil {
			return prev, cursor, err
		}
	}
	return u, next, nil
}

func modelKey(provider, model string) string {
	if provider == "" {
		return model
	}
	return provider + "/" + model
}

func readModels(ctx context.Context, db *sql.DB, layout databaseLayout, id string, u *store.Usage) error {
	rows, err := db.QueryContext(ctx, layout.tokensByModel, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var provider, model string
		var requests int
		var cost float64
		var input, output, reasoning, cacheRead, cacheWrite int64
		if err := rows.Scan(&provider, &model, &requests, &cost, &input, &output, &reasoning, &cacheRead, &cacheWrite); err != nil {
			return err
		}
		t := store.Tokens{Input: input, Output: output + reasoning, Reasoning: reasoning, CacheRead: cacheRead, CacheWrite: cacheWrite}
		u.Models[modelKey(provider, model)] = u.Models[modelKey(provider, model)].Add(t)
		u.Tokens = u.Tokens.Add(t)
		u.Requests += requests
		u.CostUSD += cost
	}
	return rows.Err()
}

func readLastMessage(ctx context.Context, db *sql.DB, layout databaseLayout, id string, u *store.Usage) error {
	var data string
	err := db.QueryRowContext(ctx, layout.lastAssistantMessage, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	msg := gjson.Parse(data)
	u.Model = modelKey(msg.Get(layout.providerPath).String(), msg.Get(layout.modelPath).String())
	u.Effort = msg.Get(layout.effortPath).String()
	tokens := msg.Get("tokens")
	u.ContextTokens = tokens.Get("input").Int() + tokens.Get("cache.read").Int() + tokens.Get("cache.write").Int()
	return nil
}

func readTools(ctx context.Context, db *sql.DB, layout databaseLayout, id string, u *store.Usage) error {
	return countRows(ctx, db, u.Tools, layout.toolCounts, id)
}

func readSkills(ctx context.Context, db *sql.DB, layout databaseLayout, id string, u *store.Usage) error {
	return countRows(ctx, db, u.Skills, layout.skillCounts, id)
}

func readCompactions(ctx context.Context, db *sql.DB, layout databaseLayout, id string, u *store.Usage) error {
	return db.QueryRowContext(ctx, layout.compactionCount, id).Scan(&u.Compactions)
}

func countRows(ctx context.Context, db *sql.DB, into map[string]int, query, id string) error {
	rows, err := db.QueryContext(ctx, query, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return err
		}
		if name != "" {
			into[name] += n
		}
	}
	return rows.Err()
}
