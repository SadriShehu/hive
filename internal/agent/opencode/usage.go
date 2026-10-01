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
	var updated int64
	err = db.QueryRowContext(ctx, `SELECT time_updated FROM session WHERE id = ?`, s.NativeID).Scan(&updated)
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
	for _, read := range []func(context.Context, *sql.DB, string, *store.Usage) error{readModels, readLastMessage, readTools, readSkills, readCompactions} {
		if err := read(ctx, db, s.NativeID, &u); err != nil {
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

func readModels(ctx context.Context, db *sql.DB, id string, u *store.Usage) error {
	rows, err := db.QueryContext(ctx, `SELECT COALESCE(json_extract(data, '$.providerID'), ''), COALESCE(json_extract(data, '$.modelID'), ''),
			COUNT(*), COALESCE(SUM(json_extract(data, '$.cost')), 0),
			COALESCE(SUM(json_extract(data, '$.tokens.input')), 0), COALESCE(SUM(json_extract(data, '$.tokens.output')), 0),
			COALESCE(SUM(json_extract(data, '$.tokens.reasoning')), 0),
			COALESCE(SUM(json_extract(data, '$.tokens.cache.read')), 0), COALESCE(SUM(json_extract(data, '$.tokens.cache.write')), 0)
		FROM message WHERE session_id = ? AND json_extract(data, '$.role') = 'assistant' GROUP BY 1, 2`, id)
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

func readLastMessage(ctx context.Context, db *sql.DB, id string, u *store.Usage) error {
	var data string
	err := db.QueryRowContext(ctx, `SELECT data FROM message WHERE session_id = ? AND json_extract(data, '$.role') = 'assistant'
		ORDER BY time_created DESC, id DESC LIMIT 1`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	msg := gjson.Parse(data)
	u.Model = modelKey(msg.Get("providerID").String(), msg.Get("modelID").String())
	u.Effort = msg.Get("variant").String()
	tokens := msg.Get("tokens")
	u.ContextTokens = tokens.Get("input").Int() + tokens.Get("cache.read").Int() + tokens.Get("cache.write").Int()
	return nil
}

func readTools(ctx context.Context, db *sql.DB, id string, u *store.Usage) error {
	return countRows(ctx, db, u.Tools, `SELECT COALESCE(json_extract(data, '$.tool'), ''), COUNT(*) FROM part
		WHERE session_id = ? AND json_extract(data, '$.type') = 'tool' GROUP BY 1`, id)
}

func readSkills(ctx context.Context, db *sql.DB, id string, u *store.Usage) error {
	return countRows(ctx, db, u.Skills, `SELECT COALESCE(json_extract(data, '$.state.input.name'), ''), COUNT(*) FROM part
		WHERE session_id = ? AND json_extract(data, '$.tool') = 'skill' GROUP BY 1`, id)
}

func readCompactions(ctx context.Context, db *sql.DB, id string, u *store.Usage) error {
	return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message WHERE session_id = ? AND json_type(data, '$.summary') = 'true'`, id).Scan(&u.Compactions)
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
