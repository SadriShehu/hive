package codex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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

func (a *Adapter) Import(ctx context.Context, st *store.Store, found func(agent.ShellCommand)) (int, error) {
	n := 0
	var errs []error
	for _, path := range a.rolloutFiles() {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		key := "codex:" + path
		stamp := fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
		if v, ok, _ := st.ImportState(key); ok && v == stamp {
			continue
		}
		s, commands, ok, err := readRollout(path, fi.ModTime())
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		if ok {
			if err := st.Upsert(s); err != nil {
				return n, err
			}
			for _, c := range commands {
				found(agent.ShellCommand{SessionID: s.ID, At: c.at, Cwd: c.cwd, Command: c.command})
			}
			n++
		}
		if err := st.SetImportState(key, stamp); err != nil {
			return n, err
		}
	}
	if err := a.importState(ctx, st); err != nil {
		errs = append(errs, fmt.Errorf("state database: %w", err))
	}
	return n, errors.Join(errs...)
}

func (a *Adapter) rolloutFiles() []string {
	var out []string
	for _, pattern := range a.rolloutGlobs() {
		matches, _ := filepath.Glob(pattern)
		out = append(out, matches...)
	}
	return out
}

type shellCommand struct {
	at      int64
	cwd     string
	command string
}

type rollout struct {
	id, cwd, cwdNow         string
	source, originator      string
	threadSource, agentRole string
	created, updated        int64
	firstPrompt, lastPrompt string
	spoke                   bool
	executed, called        []shellCommand
}

func readRollout(path string, mtime time.Time) (store.Session, []shellCommand, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return store.Session{}, nil, false, err
	}
	defer f.Close()
	r := &rollout{}
	reader := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if rec, ok := parseLine(line); ok {
				r.record(rec)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return store.Session{}, nil, false, err
		}
	}
	id := firstNonEmpty(r.id, idFromFilename(path))
	if id == "" || (r.firstPrompt == "" && !r.spoke) {
		return store.Session{}, nil, false, nil
	}
	if r.created == 0 {
		r.created = mtime.UnixMilli()
	}
	s := store.Session{
		ID: store.ID(name, id), Tool: name, NativeID: id, Cwd: r.cwd,
		Kind: kindOf(r.source, r.originator, r.threadSource, r.agentRole), Status: store.StatusExited,
		Transcript: path, LastPrompt: firstNonEmpty(r.lastPrompt, r.firstPrompt),
		CreatedAt: r.created, UpdatedAt: max(r.updated, r.created), Source: "import",
	}
	commands := r.executed
	if len(commands) == 0 {
		commands = r.called
	}
	return s, commands, true, nil
}

func idFromFilename(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if len(base) < 36 {
		return ""
	}
	return base[len(base)-36:]
}

func (r *rollout) record(rec record) {
	if rec.at > 0 {
		if r.created == 0 {
			r.created = rec.at
		}
		r.updated = max(r.updated, rec.at)
	}
	switch {
	case rec.kind == "session_meta":
		r.meta(rec.payload)
	case rec.kind == "turn_context":
		r.cwdNow = firstNonEmpty(rec.payload.Get("cwd").String(), r.cwdNow)
	case rec.kind == "event_msg":
		r.event(rec.payload, rec.at)
	case rec.isItem():
		r.item(rec.payload, rec.at)
	}
}

func (r *rollout) meta(payload gjson.Result) {
	r.id = firstNonEmpty(payload.Get("id").String(), payload.Get("session_id").String(), r.id)
	r.cwd = firstNonEmpty(payload.Get("cwd").String(), r.cwd)
	r.cwdNow = firstNonEmpty(r.cwdNow, r.cwd)
	r.source = firstNonEmpty(sourceName(payload.Get("source")), r.source)
	r.originator = firstNonEmpty(payload.Get("originator").String(), r.originator)
	r.threadSource = firstNonEmpty(payload.Get("thread_source").String(), r.threadSource)
	r.agentRole = firstNonEmpty(payload.Get("agent_role").String(), r.agentRole)
	if started := timestamp(payload.Get("timestamp").String()); started > 0 {
		if r.created == 0 || started < r.created {
			r.created = started
		}
	}
}

func (r *rollout) item(payload gjson.Result, at int64) {
	switch payload.Get("type").String() {
	case "message":
		text := messageText(payload.Get("content"))
		switch payload.Get("role").String() {
		case "user":
			r.prompt(text)
		case "assistant":
			r.spoke = true
		}
	case "function_call":
		command, cwd := callCommand(payload.Get("arguments"))
		r.call(at, command, cwd)
	case "local_shell_call":
		action := payload.Get("action")
		r.call(at, commandString(action.Get("command")), action.Get("working_directory").String())
	}
}

func (r *rollout) event(payload gjson.Result, at int64) {
	switch payload.Get("type").String() {
	case "exec_command_begin":
		r.execute(at, commandString(payload.Get("command")), payload.Get("cwd").String())
	case "item_completed":
		item := payload.Get("item")
		if item.Get("type").String() == "CommandExecution" {
			r.execute(at, commandString(item.Get("command")), fileURLPath(item.Get("cwd").String()))
		}
	case "user_message":
		r.prompt(cleanText(payload.Get("message").String()))
	case "agent_message":
		r.spoke = true
	}
}

func (r *rollout) prompt(text string) {
	if text == "" {
		return
	}
	if r.firstPrompt == "" {
		r.firstPrompt = text
	}
	r.lastPrompt = text
}

func (r *rollout) call(at int64, command, cwd string) {
	if command != "" {
		r.called = append(r.called, shellCommand{at: at, cwd: firstNonEmpty(cwd, r.cwdNow, r.cwd), command: command})
	}
}

func (r *rollout) execute(at int64, command, cwd string) {
	if command != "" {
		r.executed = append(r.executed, shellCommand{at: at, cwd: firstNonEmpty(cwd, r.cwdNow, r.cwd), command: command})
	}
}

func (a *Adapter) importState(ctx context.Context, st *store.Store) error {
	path := a.stateDBPath()
	if path == "" {
		return nil
	}
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
	columns, err := tableColumns(ctx, db, "threads")
	if err != nil {
		return err
	}
	if !columns["id"] {
		return nil
	}
	if err := a.importThreads(ctx, db, st, columns); err != nil {
		return err
	}
	return importSpawnEdges(ctx, db, st)
}

func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func (a *Adapter) importThreads(ctx context.Context, db *sql.DB, st *store.Store, columns map[string]bool) error {
	text := func(column string) string {
		if columns[column] {
			return "COALESCE(" + column + ", '')"
		}
		return "''"
	}
	millis := func(column string) string {
		expr := "0"
		if columns[column] {
			expr = "COALESCE(" + column + ", 0) * 1000"
		}
		if columns[column+"_ms"] {
			expr = "COALESCE(" + column + "_ms, " + expr + ")"
		}
		return expr
	}
	query := fmt.Sprintf(`SELECT id, %s, %s, %s, %s, %s, %s, %s, %s, %s FROM threads WHERE %s >= ? ORDER BY %s, id`,
		text("name"), text("cwd"), text("first_user_message"), text("rollout_path"), text("source"),
		text("thread_source"), text("agent_role"), millis("created_at"), millis("updated_at"),
		millis("updated_at"), millis("updated_at"))
	cursor, _, err := st.ImportState("codex:state-cursor")
	if err != nil {
		return err
	}
	since, _ := strconv.ParseInt(cursor, 10, 64)
	rows, err := db.QueryContext(ctx, query, since)
	if err != nil {
		return err
	}
	type thread struct {
		id, name, cwd, firstMessage, rolloutPath, source, threadSource, agentRole string
		created, updated                                                          int64
	}
	var threads []thread
	for rows.Next() {
		var t thread
		if err := rows.Scan(&t.id, &t.name, &t.cwd, &t.firstMessage, &t.rolloutPath, &t.source,
			&t.threadSource, &t.agentRole, &t.created, &t.updated); err != nil {
			rows.Close()
			return err
		}
		threads = append(threads, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	next := since
	for _, t := range threads {
		if err := ctx.Err(); err != nil {
			return err
		}
		next = max(next, t.updated)
		if t.id == "" {
			continue
		}
		sum := sha256.Sum256([]byte(strings.Join([]string{t.name, t.cwd, t.firstMessage, t.rolloutPath, t.source,
			t.threadSource, t.agentRole, strconv.FormatInt(t.created, 10), strconv.FormatInt(t.updated, 10)}, "\x00")))
		stamp := hex.EncodeToString(sum[:])
		key := "codex:state:" + t.id
		if old, ok, err := st.ImportState(key); err != nil {
			return err
		} else if ok && old == stamp {
			continue
		}
		id := store.ID(name, t.id)
		_, known, err := st.Get(id)
		if err != nil {
			return err
		}
		if known || t.firstMessage != "" || t.name != "" {
			s := store.Session{
				ID: id, Tool: name, NativeID: t.id, Title: t.name, Cwd: t.cwd,
				Kind: kindOf(t.source, "", t.threadSource, t.agentRole), Status: store.StatusExited,
				Transcript: t.rolloutPath, CreatedAt: t.created, UpdatedAt: max(t.updated, t.created), Source: "import",
			}
			if s.Kind == store.KindInteractive {
				s.Kind = ""
			}
			if !known {
				s.LastPrompt = t.firstMessage
			}
			if err := st.Upsert(s); err != nil {
				return err
			}
		}
		if err := st.SetImportState(key, stamp); err != nil {
			return err
		}
	}
	return st.SetImportState("codex:state-cursor", strconv.FormatInt(next, 10))
}

func importSpawnEdges(ctx context.Context, db *sql.DB, st *store.Store) error {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'thread_spawn_edges'`).Scan(&count)
	if err != nil || count == 0 {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT parent_thread_id, child_thread_id FROM thread_spawn_edges`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var parent, child string
		if err := rows.Scan(&parent, &child); err != nil {
			return err
		}
		childID := store.ID(name, child)
		if _, ok, err := st.Get(childID); err != nil || !ok {
			if err != nil {
				return err
			}
			continue
		}
		if _, err := st.SetParent(childID, store.ID(name, parent)); err != nil {
			return err
		}
		if err := st.SetKindIfUnknown(childID, store.KindInternal); err != nil {
			return err
		}
	}
	return rows.Err()
}
