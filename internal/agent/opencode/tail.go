package opencode

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

type tailLine = agent.Line

// Tail returns the last n entries of a session, read from opencode's database.
func (a *Adapter) Tail(s store.Session, n int) ([]agent.Line, error) {
	db, err := a.openDB(2 * time.Second)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	layout, err := layoutOf(context.Background(), db)
	if err != nil {
		return nil, err
	}
	rows, err := layout.tail(db, s.NativeID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var newestFirst []agent.Line
	for rows.Next() && len(newestFirst) < n {
		var role, data string
		if err := rows.Scan(&role, &data); err != nil {
			return nil, err
		}
		rowLines := layout.tailLines(role, data)
		slices.Reverse(rowLines)
		newestFirst = append(newestFirst, rowLines...)
	}
	if len(newestFirst) > n {
		newestFirst = newestFirst[:n]
	}
	slices.Reverse(newestFirst)
	return newestFirst, rows.Err()
}

func openCode1TailRows(db *sql.DB, sessionID string, n int) (*sql.Rows, error) {
	// Reasoning and step markers are skipped, so read more parts than lines.
	return db.Query(`SELECT COALESCE(json_extract(m.data, '$.role'), ''), p.data
		FROM part p JOIN message m ON m.id = p.message_id
		WHERE p.session_id = ? ORDER BY p.time_created DESC, p.id DESC LIMIT ?`, sessionID, n*4)
}

func openCode1TailLines(role, data string) []tailLine {
	part := gjson.Parse(data)
	switch part.Get("type").String() {
	case "text":
		if text := strings.TrimSpace(part.Get("text").String()); text != "" && !part.Get("synthetic").Bool() {
			return []tailLine{{Role: role, Text: text}}
		}
	case "tool":
		return []tailLine{{Role: "tool", Text: toolSummary(part.Get("tool").String(), part.Get("state"))}}
	}
	return nil
}

func openCode2TailRows(db *sql.DB, sessionID string, n int) (*sql.Rows, error) {
	return db.Query(`SELECT type, data FROM session_message
		WHERE session_id = ? AND type IN ('user', 'assistant') ORDER BY seq DESC LIMIT ?`, sessionID, n*4)
}

func openCode2TailLines(role, data string) []tailLine {
	message := gjson.Parse(data)
	if role == "user" {
		if text := strings.TrimSpace(message.Get("text").String()); text != "" {
			return []tailLine{{Role: role, Text: text}}
		}
		return nil
	}
	var lines []tailLine
	for _, content := range message.Get("content").Array() {
		switch content.Get("type").String() {
		case "text":
			if text := strings.TrimSpace(content.Get("text").String()); text != "" {
				lines = append(lines, tailLine{Role: role, Text: text})
			}
		case "tool":
			lines = append(lines, tailLine{Role: "tool", Text: toolSummary(content.Get("name").String(), content.Get("state"))})
		}
	}
	return lines
}

func toolSummary(name string, state gjson.Result) string {
	if title := state.Get("title").String(); strings.TrimSpace(title) != "" {
		title, _, _ = strings.Cut(strings.TrimSpace(title), "\n")
		return name + ": " + title
	}
	input := state.Get("input")
	for _, key := range []string{"description", "command", "filePath", "path", "pattern", "url", "query", "id"} {
		if v := input.Get(key).String(); v != "" {
			v, _, _ = strings.Cut(v, "\n")
			return name + ": " + v
		}
	}
	return name
}
