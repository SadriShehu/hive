package claude

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

// Import reads every transcript under the projects directory: one per
// session, plus one per Task subagent. Unchanged files are skipped.
func (a *Adapter) Import(ctx context.Context, st *store.Store, found func(agent.ShellCommand)) (int, error) {
	dir := a.projectsDir()
	top, _ := filepath.Glob(filepath.Join(dir, "*", "*.jsonl"))
	subs, _ := filepath.Glob(filepath.Join(dir, "*", "*", "subagents", "agent-*.jsonl"))
	n := 0
	var errs []error
	for _, path := range append(top, subs...) {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		key := "claude:" + path
		stamp := fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
		if v, ok, _ := st.ImportState(key); ok && v == stamp {
			continue
		}
		s, ok, err := readTranscript(path, fi.ModTime(), found)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		if ok {
			if err := st.Upsert(s); err != nil {
				return n, err
			}
			n++
		}
		if err := st.SetImportState(key, stamp); err != nil {
			return n, err
		}
	}
	return n, errors.Join(errs...)
}

// transcript accumulates what one transcript file says about its session.
type transcript struct {
	id, parent               string
	created, updated         int64
	cwd, cwdNow              string
	entrypoint               string
	custom, named, aiTitle   string
	firstPrompt, lastPrompt  string
	spoke                    bool // any assistant turn
	found                    func(agent.ShellCommand)
}

// readTranscript reads a session from its transcript. ok is false for a
// transcript with nothing in it worth listing.
func readTranscript(path string, mtime time.Time, found func(agent.ShellCommand)) (store.Session, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return store.Session{}, false, err
	}
	defer f.Close()

	t := &transcript{found: found}
	s := store.Session{Tool: name, Status: store.StatusExited, Transcript: path, Source: "import"}
	if filepath.Base(filepath.Dir(path)) == "subagents" {
		// <project>/<session>/subagents/agent-<id>.jsonl
		session := filepath.Base(filepath.Dir(filepath.Dir(path)))
		agentID := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "agent-"), ".jsonl")
		s.NativeID = session + "/" + agentID
		s.ParentID = store.ID(name, session)
		s.Kind = store.KindInternal
	} else {
		s.NativeID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	s.ID = store.ID(name, s.NativeID)
	t.id = s.ID

	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			t.line(line)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return store.Session{}, false, err
		}
	}

	if t.firstPrompt == "" && t.aiTitle == "" && t.custom == "" && !t.spoke {
		return store.Session{}, false, nil
	}
	s.Cwd = t.cwd
	s.CreatedAt = t.created
	s.UpdatedAt = max(t.updated, t.created)
	if s.CreatedAt == 0 {
		s.CreatedAt, s.UpdatedAt = mtime.UnixMilli(), mtime.UnixMilli()
	}
	s.Title = firstNonEmpty(t.custom, t.named, t.aiTitle)
	s.LastPrompt = firstNonEmpty(t.lastPrompt, t.firstPrompt)
	if s.Kind == store.KindInternal {
		s.Title = firstNonEmpty(subagentTitle(path, ""), clip(t.firstPrompt, 120))
	} else if strings.HasPrefix(t.entrypoint, "sdk") {
		s.Kind = store.KindHeadless // claude -p, the Agent SDK
	} else {
		s.Kind = store.KindInteractive
	}
	return s, true, nil
}

func (t *transcript) line(line []byte) {
	f := gjson.GetManyBytes(line, "type", "timestamp", "cwd", "entrypoint")
	var at int64
	if ts, err := time.Parse(time.RFC3339Nano, f[1].String()); err == nil {
		at = ts.UnixMilli()
		if t.created == 0 {
			t.created = at
		}
		t.updated = max(t.updated, at)
	}
	if cwd := f[2].String(); cwd != "" {
		if t.cwd == "" {
			t.cwd = cwd
		}
		t.cwdNow = cwd
	}
	if t.entrypoint == "" {
		t.entrypoint = f[3].String()
	}
	switch f[0].String() {
	case "custom-title":
		t.custom = gjson.GetBytes(line, "customTitle").String()
	case "agent-name":
		t.named = gjson.GetBytes(line, "agentName").String()
	case "ai-title":
		t.aiTitle = gjson.GetBytes(line, "aiTitle").String()
	case "last-prompt":
		t.lastPrompt = gjson.GetBytes(line, "lastPrompt").String()
	case "user":
		if t.firstPrompt == "" {
			t.firstPrompt = userText(line)
		}
	case "assistant":
		t.spoke = true
		if !bytes.Contains(line, []byte(`"name":"Bash"`)) {
			return
		}
		gjson.GetBytes(line, "message.content").ForEach(func(_, c gjson.Result) bool {
			if c.Get("type").String() == "tool_use" && c.Get("name").String() == "Bash" {
				t.found(agent.ShellCommand{SessionID: t.id, At: at, Cwd: t.cwdNow, Command: c.Get("input.command").String()})
			}
			return true
		})
	}
}

// userText returns what the user typed on a user line, or "" for tool
// results, injected context and command wrappers.
func userText(line []byte) string {
	if gjson.GetBytes(line, "isMeta").Bool() {
		return ""
	}
	content := gjson.GetBytes(line, "message.content")
	text := content.String()
	if content.IsArray() {
		text = ""
		for _, c := range content.Array() {
			switch c.Get("type").String() {
			case "tool_result":
				return ""
			case "text":
				if text == "" {
					text = c.Get("text").String()
				}
			}
		}
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "<") || strings.HasPrefix(text, "Caveat:") || strings.HasPrefix(text, "[Request interrupted") {
		return ""
	}
	return text
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
