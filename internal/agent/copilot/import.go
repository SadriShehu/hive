package copilot

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

// Import reads Copilot's session database and event logs without opening either
// database for writing. Per-session state prevents unchanged history being read.
func (a *Adapter) Import(ctx context.Context, st *store.Store, found func(agent.ShellCommand)) (int, error) {
	changed := make(map[string]bool)
	emitted := make(map[string]bool)
	uniqueFound := func(c agent.ShellCommand) {
		key := fmt.Sprintf("%s\x00%d\x00%s\x00%s", c.SessionID, c.At, c.Cwd, c.Command)
		if !emitted[key] {
			emitted[key] = true
			found(c)
		}
	}
	if err := a.importDB(ctx, st, changed); err != nil {
		return len(changed), err
	}
	if err := a.importEventFiles(ctx, st, uniqueFound, changed); err != nil {
		return len(changed), err
	}
	return len(changed), nil
}

func (a *Adapter) importDB(ctx context.Context, st *store.Store, changed map[string]bool) error {
	path := a.dbPath()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer db.Close()
	cursor, ok, err := st.ImportState("copilot:db-cursor")
	if err != nil {
		return err
	}
	if !ok {
		cursor = ""
	}
	rows, err := db.QueryContext(ctx, `SELECT id, COALESCE(cwd, ''), COALESCE(summary, ''),
		created_at, updated_at FROM sessions WHERE updated_at >= ? ORDER BY updated_at, id`, cursor)
	if err != nil {
		return err
	}
	type sessionRow struct {
		id, cwd, title, created, updated string
	}
	var sessions []sessionRow
	for rows.Next() {
		var r sessionRow
		if err := rows.Scan(&r.id, &r.cwd, &r.title, &r.created, &r.updated); err != nil {
			rows.Close()
			return err
		}
		sessions = append(sessions, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	nextCursor := cursor
	for _, r := range sessions {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.id == "" {
			return errors.New("Copilot session store contains an empty session ID")
		}
		if r.updated > nextCursor {
			nextCursor = r.updated
		}
		var prompt, promptAt string
		var turn int64
		err := db.QueryRowContext(ctx, `SELECT COALESCE(user_message, ''), COALESCE(timestamp, ''), turn_index
			FROM turns WHERE session_id = ? AND user_message IS NOT NULL AND user_message <> ''
			ORDER BY turn_index DESC LIMIT 1`, r.id).Scan(&prompt, &promptAt, &turn)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		stampData := strings.Join([]string{r.cwd, r.title, r.created, r.updated, promptAt, strconv.FormatInt(turn, 10), prompt}, "\x00")
		sum := sha256.Sum256([]byte(stampData))
		stamp := hex.EncodeToString(sum[:])
		key := "copilot:db:" + r.id
		if old, ok, err := st.ImportState(key); err != nil {
			return err
		} else if ok && old == stamp {
			continue
		}
		created, updated := timestamp(r.created), timestamp(r.updated)
		if updated == 0 {
			updated = created
		}
		session := store.Session{
			ID: store.ID(name, r.id), Tool: name, NativeID: r.id, Title: r.title, Cwd: r.cwd,
			Kind: store.KindInteractive, Status: store.StatusExited,
			Transcript: filepath.Join(a.sessionStateDir(), r.id, "events.jsonl"),
			LastPrompt: prompt, CreatedAt: created, UpdatedAt: updated, Source: store.SourceImport,
		}
		if err := st.Upsert(session); err != nil {
			return err
		}
		if err := st.SetImportState(key, stamp); err != nil {
			return err
		}
		changed[session.ID] = true
	}
	return st.SetImportState("copilot:db-cursor", nextCursor)
}

func (a *Adapter) importEventFiles(ctx context.Context, st *store.Store, found func(agent.ShellCommand), changed map[string]bool) error {
	entries, err := os.ReadDir(a.sessionStateDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		dir := filepath.Join(a.sessionStateDir(), id)
		eventPath := filepath.Join(dir, "events.jsonl")
		info, err := os.Stat(eventPath)
		if errors.Is(err, fs.ErrNotExist) {
			info = nil
		} else if err != nil {
			return err
		}
		stamp := "missing"
		if info != nil {
			stamp = fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
		}
		key := "copilot:events:" + id
		if old, ok, err := st.ImportState(key); err != nil {
			return err
		} else if ok && old == stamp {
			continue
		}
		workspace, err := readWorkspace(filepath.Join(dir, "workspace.yaml"))
		if err != nil {
			return fmt.Errorf("%s: %w", dir, err)
		}
		prompt, created, updated, cwd, eventID, commands, err := readEvents(eventPath, id)
		if err != nil {
			return fmt.Errorf("%s: %w", eventPath, err)
		}
		if cwd == "" {
			cwd = workspace.cwd
		}
		title := firstNonEmpty(workspace.summary, workspace.name)
		existing, ok, err := st.Get(store.ID(name, id))
		if err != nil {
			return err
		}
		if workspace.userNamed && workspace.name != "" {
			title = workspace.name
		} else if ok && existing.Title != "" {
			title = ""
		}
		if created == 0 {
			created = timestamp(workspace.created)
		}
		if updated == 0 {
			updated = timestamp(workspace.updated)
		}
		updated = max(updated, created)
		sessionID := firstNonEmpty(eventID, id)
		session := store.Session{
			ID: store.ID(name, sessionID), Tool: name, NativeID: sessionID, Title: title,
			Cwd: cwd, Kind: store.KindInteractive, Status: store.StatusExited,
			Transcript: eventPath, LastPrompt: prompt,
			CreatedAt: created, UpdatedAt: updated, Source: store.SourceImport,
		}
		if session.CreatedAt == 0 || session.UpdatedAt == 0 {
			if info != nil {
				fallback := info.ModTime().UnixMilli()
				if session.CreatedAt == 0 {
					session.CreatedAt = fallback
				}
				if session.UpdatedAt == 0 {
					session.UpdatedAt = fallback
				}
			}
		}
		if err := st.Upsert(session); err != nil {
			return err
		}
		for _, command := range commands {
			found(agent.ShellCommand{SessionID: session.ID, At: command.at, Cwd: firstNonEmpty(command.cwd, cwd), Command: command.command})
		}
		if err := st.SetImportState(key, stamp); err != nil {
			return err
		}
		changed[session.ID] = true
	}
	return nil
}

type workspace struct {
	cwd, name, summary, created, updated string
	userNamed                            bool
}

func readWorkspace(path string) (workspace, error) {
	var w workspace
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return w, nil
	}
	if err != nil {
		return w, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = yamlScalar(value)
		switch strings.TrimSpace(key) {
		case "cwd":
			w.cwd = value
		case "name":
			w.name = value
		case "summary":
			w.summary = value
		case "created_at":
			w.created = value
		case "updated_at":
			w.updated = value
		case "user_named":
			w.userNamed = value == "true"
		}
	}
	return w, scanner.Err()
}

func yamlScalar(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	return value
}

type shellCommand struct {
	at      int64
	cwd     string
	command string
}

func readEvents(path, id string) (string, int64, int64, string, string, []shellCommand, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", 0, 0, "", "", nil, nil
	}
	if err != nil {
		return "", 0, 0, "", "", nil, err
	}
	defer f.Close()
	var (
		prompt, cwd, sessionID string
		created, updated       int64
		commands               []shellCommand
	)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), 64<<20)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if !gjson.ValidBytes(line) {
			return "", 0, 0, "", "", nil, fmt.Errorf("invalid JSON at line %d", lineNumber)
		}
		typ := gjson.GetBytes(line, "type").String()
		at := timestamp(gjson.GetBytes(line, "timestamp").String())
		updated = max(updated, at)
		if eventSession := gjson.GetBytes(line, "data.sessionId").String(); eventSession != "" {
			sessionID = eventSession
		}
		switch typ {
		case "session.start":
			start := timestamp(gjson.GetBytes(line, "data.startTime").String())
			if start == 0 {
				start = at
			}
			created = start
			cwd = firstNonEmpty(gjson.GetBytes(line, "data.context.cwd").String(), cwd)
		case "session.resume":
			cwd = firstNonEmpty(gjson.GetBytes(line, "data.context.cwd").String(), cwd)
		case "session.context_changed":
			cwd = firstNonEmpty(gjson.GetBytes(line, "data.cwd").String(), cwd)
		case "user.message":
			if text := strings.TrimSpace(gjson.GetBytes(line, "data.content").String()); text != "" {
				prompt = text
			}
		case "tool.execution_start":
			tool := strings.ToLower(gjson.GetBytes(line, "data.toolName").String())
			args := gjson.GetBytes(line, "data.arguments")
			if tool != "bash" && tool != "powershell" {
				continue
			}
			command := firstNonEmpty(args.Get("command").String(), args.Get("script").String())
			if command != "" {
				commands = append(commands, shellCommand{
					at: at, cwd: firstNonEmpty(args.Get("workdir").String(), args.Get("cwd").String()),
					command: command,
				})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", 0, 0, "", "", nil, err
	}
	return prompt, created, updated, cwd, firstNonEmpty(sessionID, id), commands, nil
}

func timestamp(value string) int64 {
	if value == "" {
		return 0
	}
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		if n < 1e12 {
			n *= 1000
		}
		return n
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err == nil {
		return t.UnixMilli()
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
		if t, err = time.ParseInLocation(layout, value, time.UTC); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}
