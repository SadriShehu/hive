package opencode

import (
	"database/sql"
	"slices"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

// Tail returns the last n entries of a session, read from opencode's database.
func (a *Adapter) Tail(s store.Session, n int) ([]agent.Line, error) {
	db, err := sql.Open("sqlite", "file:"+a.dbPath()+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	// Reasoning and step markers are skipped, so read more parts than lines.
	rows, err := db.Query(`SELECT COALESCE(json_extract(m.data, '$.role'), ''), p.data
		FROM part p JOIN message m ON m.id = p.message_id
		WHERE p.session_id = ? ORDER BY p.time_created DESC, p.id DESC LIMIT ?`, s.NativeID, n*4)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var lines []agent.Line
	for rows.Next() && len(lines) < n {
		var role, data string
		if err := rows.Scan(&role, &data); err != nil {
			return nil, err
		}
		part := gjson.Parse(data)
		switch part.Get("type").String() {
		case "text":
			if text := strings.TrimSpace(part.Get("text").String()); text != "" && !part.Get("synthetic").Bool() {
				lines = append(lines, agent.Line{Role: role, Text: text})
			}
		case "tool":
			lines = append(lines, agent.Line{Role: "tool", Text: toolSummary(part)})
		}
	}
	slices.Reverse(lines)
	return lines, rows.Err()
}

func toolSummary(part gjson.Result) string {
	name := part.Get("tool").String()
	if title := part.Get("state.title").String(); strings.TrimSpace(title) != "" {
		title, _, _ = strings.Cut(strings.TrimSpace(title), "\n")
		return name + ": " + title
	}
	input := part.Get("state.input")
	for _, key := range []string{"description", "command", "filePath", "pattern", "url", "query"} {
		if v := input.Get(key).String(); v != "" {
			v, _, _ = strings.Cut(v, "\n")
			return name + ": " + v
		}
	}
	return name
}
