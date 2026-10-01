package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type Tokens struct {
	Input        int64 `json:"input"`
	Output       int64 `json:"output"`
	CacheRead    int64 `json:"cache_read"`
	CacheWrite   int64 `json:"cache_write"`
	CacheWrite1h int64 `json:"cache_write_1h"`
	Reasoning    int64 `json:"reasoning"`
}

func (t Tokens) Add(o Tokens) Tokens {
	return Tokens{
		Input:        t.Input + o.Input,
		Output:       t.Output + o.Output,
		CacheRead:    t.CacheRead + o.CacheRead,
		CacheWrite:   t.CacheWrite + o.CacheWrite,
		CacheWrite1h: t.CacheWrite1h + o.CacheWrite1h,
		Reasoning:    t.Reasoning + o.Reasoning,
	}
}

func (t Tokens) IsZero() bool { return t == Tokens{} }

const (
	CostTool   = "tool"
	CostConfig = "config"
)

type Usage struct {
	ID              string            `json:"id"`
	Model           string            `json:"model,omitempty"`
	Models          map[string]Tokens `json:"models,omitempty"`
	Tokens          Tokens            `json:"tokens"`
	CostUSD         float64           `json:"cost_usd"`
	CostSource      string            `json:"cost_source,omitempty"`
	ContextTokens   int64             `json:"context_tokens"`
	ContextWindow   int64             `json:"context_window,omitempty"`
	Tools           map[string]int    `json:"tools,omitempty"`
	Skills          map[string]int    `json:"skills,omitempty"`
	Requests        int               `json:"requests"`
	Compactions     int               `json:"compactions,omitempty"`
	Effort          string            `json:"effort,omitempty"`
	APIDurationMS   int64             `json:"api_duration_ms,omitempty"`
	LinesAdded      int               `json:"lines_added,omitempty"`
	LinesRemoved    int               `json:"lines_removed,omitempty"`
	PremiumRequests float64           `json:"premium_requests,omitempty"`
	Partial         bool              `json:"partial,omitempty"`
	UpdatedAt       int64             `json:"updated_at"`
}

func (u Usage) CostCoversChildren() bool {
	return u.CostSource == CostTool && strings.HasPrefix(u.ID, "claude:")
}

func (u Usage) Empty() bool {
	return u.Model == "" && u.Requests == 0 && u.Tokens.IsZero() && u.CostUSD == 0 &&
		len(u.Tools) == 0 && len(u.Skills) == 0 && u.PremiumRequests == 0
}

type UsageRecord struct {
	Usage
	Cursor  string
	Version int
}

const usageColumns = `id, model, models, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
	cache_write_1h_tokens, reasoning_tokens, cost_usd, cost_source, context_tokens, context_window, tools, skills,
	requests, compactions, effort, api_duration_ms, lines_added, lines_removed, premium_requests, partial,
	cursor, version, updated_at`

func (s *Store) UpsertUsage(r UsageRecord) error {
	_, err := s.db.Exec(`INSERT INTO session_usage (`+usageColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			model = excluded.model, models = excluded.models,
			input_tokens = excluded.input_tokens, output_tokens = excluded.output_tokens,
			cache_read_tokens = excluded.cache_read_tokens, cache_write_tokens = excluded.cache_write_tokens,
			cache_write_1h_tokens = excluded.cache_write_1h_tokens, reasoning_tokens = excluded.reasoning_tokens,
			cost_usd = excluded.cost_usd, cost_source = excluded.cost_source,
			context_tokens = excluded.context_tokens, context_window = excluded.context_window,
			tools = excluded.tools, skills = excluded.skills,
			requests = excluded.requests, compactions = excluded.compactions, effort = excluded.effort,
			api_duration_ms = excluded.api_duration_ms, lines_added = excluded.lines_added,
			lines_removed = excluded.lines_removed, premium_requests = excluded.premium_requests,
			partial = excluded.partial, cursor = excluded.cursor, version = excluded.version,
			updated_at = excluded.updated_at`,
		r.ID, r.Model, jsonText(r.Models), r.Tokens.Input, r.Tokens.Output, r.Tokens.CacheRead, r.Tokens.CacheWrite,
		r.Tokens.CacheWrite1h, r.Tokens.Reasoning, r.CostUSD, r.CostSource, r.ContextTokens, r.ContextWindow,
		jsonText(r.Tools), jsonText(r.Skills), r.Requests, r.Compactions, r.Effort, r.APIDurationMS,
		r.LinesAdded, r.LinesRemoved, r.PremiumRequests, r.Partial, r.Cursor, r.Version, r.UpdatedAt)
	return err
}

func (s *Store) Usage(id string) (Usage, bool, error) {
	r, ok, err := s.UsageRecord(id)
	return r.Usage, ok, err
}

func (s *Store) UsageRecord(id string) (UsageRecord, bool, error) {
	rows, err := s.db.Query(`SELECT `+usageColumns+` FROM session_usage WHERE id = ?`, id)
	if err != nil {
		return UsageRecord{}, false, err
	}
	list, err := scanUsage(rows)
	if err != nil || len(list) == 0 {
		return UsageRecord{}, false, err
	}
	return list[0], true, nil
}

func (s *Store) AllUsage() (map[string]Usage, error) {
	records, err := s.UsageRecords()
	if err != nil {
		return nil, err
	}
	out := make(map[string]Usage, len(records))
	for id, r := range records {
		out[id] = r.Usage
	}
	return out, nil
}

func (s *Store) UsageRecords() (map[string]UsageRecord, error) {
	rows, err := s.db.Query(`SELECT ` + usageColumns + ` FROM session_usage`)
	if err != nil {
		return nil, err
	}
	list, err := scanUsage(rows)
	if err != nil {
		return nil, err
	}
	out := make(map[string]UsageRecord, len(list))
	for _, r := range list {
		out[r.ID] = r
	}
	return out, nil
}

func (s *Store) DeleteUsage(id string) error {
	_, err := s.db.Exec(`DELETE FROM session_usage WHERE id = ?`, id)
	return err
}

func (s *Store) ResetUsage() error {
	_, err := s.db.Exec(`DELETE FROM session_usage`)
	return err
}

func jsonText(v any) string {
	data, err := json.Marshal(v)
	if err != nil || string(data) == "null" {
		return "{}"
	}
	return string(data)
}

func scanUsage(rows *sql.Rows) ([]UsageRecord, error) {
	defer rows.Close()
	var out []UsageRecord
	for rows.Next() {
		var r UsageRecord
		var models, tools, skills string
		if err := rows.Scan(&r.ID, &r.Model, &models, &r.Tokens.Input, &r.Tokens.Output, &r.Tokens.CacheRead,
			&r.Tokens.CacheWrite, &r.Tokens.CacheWrite1h, &r.Tokens.Reasoning, &r.CostUSD, &r.CostSource,
			&r.ContextTokens, &r.ContextWindow, &tools, &skills, &r.Requests, &r.Compactions, &r.Effort,
			&r.APIDurationMS, &r.LinesAdded, &r.LinesRemoved, &r.PremiumRequests, &r.Partial,
			&r.Cursor, &r.Version, &r.UpdatedAt); err != nil {
			return nil, err
		}
		if err := errors.Join(json.Unmarshal([]byte(models), &r.Models), json.Unmarshal([]byte(tools), &r.Tools),
			json.Unmarshal([]byte(skills), &r.Skills)); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
