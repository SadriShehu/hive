package opencode

import (
	"context"
	"database/sql"
)

type databaseLayout struct {
	changedSessions      string
	sessionUpdatedAt     string
	bashCommands         string
	tokensByModel        string
	lastAssistantMessage string
	toolCounts           string
	skillCounts          string
	compactionCount      string
	providerPath         string
	modelPath            string
	effortPath           string
	tail                 func(db *sql.DB, sessionID string, n int) (*sql.Rows, error)
	tailLines            func(role, data string) []tailLine
}

var openCode1Layout = databaseLayout{
	changedSessions: `SELECT id, COALESCE(parent_id, ''), directory, COALESCE(title, ''), time_created, time_updated
		FROM session WHERE time_updated >= ? ORDER BY time_updated`,
	sessionUpdatedAt: `SELECT time_updated FROM session WHERE id = ?`,
	bashCommands: `SELECT time_created,
			COALESCE(json_extract(data, '$.state.input.command'), ''),
			COALESCE(json_extract(data, '$.state.input.workdir'), '')
		FROM part WHERE session_id = ? AND time_created >= ? AND data LIKE '%"tool":"bash"%'`,
	tokensByModel: `SELECT COALESCE(json_extract(data, '$.providerID'), ''), COALESCE(json_extract(data, '$.modelID'), ''),
			COUNT(*), COALESCE(SUM(json_extract(data, '$.cost')), 0),
			COALESCE(SUM(json_extract(data, '$.tokens.input')), 0), COALESCE(SUM(json_extract(data, '$.tokens.output')), 0),
			COALESCE(SUM(json_extract(data, '$.tokens.reasoning')), 0),
			COALESCE(SUM(json_extract(data, '$.tokens.cache.read')), 0), COALESCE(SUM(json_extract(data, '$.tokens.cache.write')), 0)
		FROM message WHERE session_id = ? AND json_extract(data, '$.role') = 'assistant' GROUP BY 1, 2`,
	lastAssistantMessage: `SELECT data FROM message WHERE session_id = ? AND json_extract(data, '$.role') = 'assistant'
		ORDER BY time_created DESC, id DESC LIMIT 1`,
	toolCounts: `SELECT COALESCE(json_extract(data, '$.tool'), ''), COUNT(*) FROM part
		WHERE session_id = ? AND json_extract(data, '$.type') = 'tool' GROUP BY 1`,
	skillCounts: `SELECT COALESCE(json_extract(data, '$.state.input.name'), ''), COUNT(*) FROM part
		WHERE session_id = ? AND json_extract(data, '$.tool') = 'skill' GROUP BY 1`,
	compactionCount: `SELECT COUNT(*) FROM message WHERE session_id = ? AND json_type(data, '$.summary') = 'true'`,
	providerPath:    "providerID",
	modelPath:       "modelID",
	effortPath:      "variant",
	tail:            openCode1TailRows,
	tailLines:       openCode1TailLines,
}

var openCode2Layout = databaseLayout{
	changedSessions: `SELECT id, COALESCE(parent_id, ''), directory, COALESCE(title, ''), time_created, time_updated
		FROM session_v2 WHERE time_updated >= ? ORDER BY time_updated`,
	sessionUpdatedAt: `SELECT time_updated FROM session_v2 WHERE id = ?`,
	bashCommands: `SELECT m.time_created,
			COALESCE(json_extract(c.value, '$.state.input.command'), ''),
			COALESCE(json_extract(c.value, '$.state.input.workdir'), '')
		FROM session_message m, json_each(m.data, '$.content') c
		WHERE m.session_id = ? AND m.time_created >= ? AND m.type = 'assistant'
			AND json_extract(c.value, '$.type') = 'tool' AND json_extract(c.value, '$.name') IN ('shell', 'bash')
		ORDER BY m.seq, c.key`,
	tokensByModel: `SELECT COALESCE(json_extract(data, '$.model.providerID'), ''), COALESCE(json_extract(data, '$.model.id'), ''),
			COUNT(*), COALESCE(SUM(json_extract(data, '$.cost')), 0),
			COALESCE(SUM(json_extract(data, '$.tokens.input')), 0), COALESCE(SUM(json_extract(data, '$.tokens.output')), 0),
			COALESCE(SUM(json_extract(data, '$.tokens.reasoning')), 0),
			COALESCE(SUM(json_extract(data, '$.tokens.cache.read')), 0), COALESCE(SUM(json_extract(data, '$.tokens.cache.write')), 0)
		FROM session_message WHERE session_id = ? AND type = 'assistant' GROUP BY 1, 2`,
	lastAssistantMessage: `SELECT data FROM session_message WHERE session_id = ? AND type = 'assistant'
		ORDER BY seq DESC LIMIT 1`,
	toolCounts: `SELECT COALESCE(json_extract(c.value, '$.name'), ''), COUNT(*)
		FROM session_message m, json_each(m.data, '$.content') c
		WHERE m.session_id = ? AND m.type = 'assistant' AND json_extract(c.value, '$.type') = 'tool' GROUP BY 1`,
	skillCounts: `SELECT COALESCE(json_extract(c.value, '$.state.input.id'), json_extract(c.value, '$.state.input.name'), ''), COUNT(*)
		FROM session_message m, json_each(m.data, '$.content') c
		WHERE m.session_id = ? AND m.type = 'assistant' AND json_extract(c.value, '$.type') = 'tool'
			AND json_extract(c.value, '$.name') = 'skill' GROUP BY 1`,
	compactionCount: `SELECT COUNT(*) FROM session_message WHERE session_id = ? AND type = 'compaction'`,
	providerPath:    "model.providerID",
	modelPath:       "model.id",
	effortPath:      "model.variant",
	tail:            openCode2TailRows,
	tailLines:       openCode2TailLines,
}

func layoutOf(ctx context.Context, db *sql.DB) (databaseLayout, error) {
	var openCode2Tables int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name IN ('session_v2', 'session_message')`).Scan(&openCode2Tables)
	if err != nil {
		return databaseLayout{}, err
	}
	if openCode2Tables == 2 {
		return openCode2Layout, nil
	}
	return openCode1Layout, nil
}
