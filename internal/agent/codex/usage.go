package codex

import (
	"context"
	"errors"
	"io/fs"
	"os"

	"github.com/tidwall/gjson"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

type usageCursor struct {
	agent.FileCursor
	LastResponse string       `json:"resp,omitempty"`
	Model        string       `json:"model,omitempty"`
	Run          store.Tokens `json:"run"`
	Records      bool         `json:"records,omitempty"`
}

func (a *Adapter) ReadUsage(ctx context.Context, s store.Session, prev store.Usage, cursor string) (store.Usage, string, error) {
	path := firstNonEmpty(s.Transcript, a.rolloutPath(s.NativeID))
	if path == "" {
		return prev, cursor, agent.ErrNoUsage
	}
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return prev, cursor, agent.ErrNoUsage
	}
	if err != nil {
		return prev, cursor, err
	}
	var c usageCursor
	if !agent.DecodeCursor(cursor, &c) || c.Behind(fi) {
		c, prev = usageCursor{}, store.Usage{}
	} else if c.Unchanged(fi) {
		return prev, cursor, nil
	}
	x := &usageScan{u: prev, c: c}
	x.u.ID = s.ID
	fc, err := agent.ScanLines(ctx, path, c.Offset, x.line)
	if err != nil {
		return prev, cursor, err
	}
	x.c.FileCursor = fc
	x.u.Model = x.c.Model
	return x.u, agent.EncodeCursor(x.c), nil
}

type usageScan struct {
	u store.Usage
	c usageCursor
}

func (x *usageScan) line(raw []byte) {
	rec, ok := parseLine(raw)
	if !ok {
		return
	}
	switch {
	case rec.kind == "turn_context":
		x.turnContext(rec.payload)
	case rec.kind == "token_usage_record":
		x.tokenRecord(rec.payload)
	case rec.kind == "event_msg" && rec.payload.Get("type").String() == "token_count":
		x.tokenCount(rec.payload.Get("info"))
	case rec.kind == "compacted":
		x.u.Compactions++
	case rec.isItem():
		x.item(rec.payload)
	}
}

func (x *usageScan) turnContext(payload gjson.Result) {
	if model := payload.Get("model").String(); model != "" {
		x.c.Model = model
	}
	if effort := payload.Get("effort").String(); effort != "" {
		x.u.Effort = effort
	}
}

func (x *usageScan) tokenRecord(payload gjson.Result) {
	id := payload.Get("response_id").String()
	if id != "" && id == x.c.LastResponse {
		return
	}
	x.c.LastResponse = id
	x.c.Records = true
	usage := payload.Get("usage")
	x.add(codexTokens(usage))
	x.u.ContextTokens = usage.Get("input_tokens").Int()
}

func (x *usageScan) tokenCount(info gjson.Result) {
	if window := info.Get("model_context_window").Int(); window > 0 {
		x.u.ContextWindow = window
	}
	if last := info.Get("last_token_usage"); last.Exists() {
		x.u.ContextTokens = last.Get("input_tokens").Int()
	}
	if x.c.Records {
		return
	}
	total := codexTokens(info.Get("total_token_usage"))
	delta, decreased := minus(total, x.c.Run)
	if decreased {
		delta = total
	}
	x.c.Run = total
	if !delta.IsZero() {
		x.add(delta)
	}
}

func (x *usageScan) item(payload gjson.Result) {
	switch payload.Get("type").String() {
	case "function_call", "custom_tool_call":
		count(&x.u.Tools, payload.Get("name").String())
	case "local_shell_call":
		count(&x.u.Tools, "shell")
	}
}

func (x *usageScan) add(t store.Tokens) {
	x.u.Tokens = x.u.Tokens.Add(t)
	x.u.Requests++
	if x.c.Model == "" {
		return
	}
	if x.u.Models == nil {
		x.u.Models = map[string]store.Tokens{}
	}
	x.u.Models[x.c.Model] = x.u.Models[x.c.Model].Add(t)
}

func codexTokens(usage gjson.Result) store.Tokens {
	cached := usage.Get("cached_input_tokens").Int()
	return store.Tokens{
		Input:      max(0, usage.Get("input_tokens").Int()-cached),
		CacheRead:  cached,
		CacheWrite: usage.Get("cache_write_input_tokens").Int(),
		Output:     usage.Get("output_tokens").Int(),
		Reasoning:  usage.Get("reasoning_output_tokens").Int(),
	}
}

func minus(a, b store.Tokens) (store.Tokens, bool) {
	d := store.Tokens{
		Input:      a.Input - b.Input,
		Output:     a.Output - b.Output,
		CacheRead:  a.CacheRead - b.CacheRead,
		CacheWrite: a.CacheWrite - b.CacheWrite,
		Reasoning:  a.Reasoning - b.Reasoning,
	}
	decreased := d.Input < 0 || d.Output < 0 || d.CacheRead < 0 || d.CacheWrite < 0 || d.Reasoning < 0
	return d, decreased
}

func count(m *map[string]int, key string) {
	if key == "" {
		return
	}
	if *m == nil {
		*m = map[string]int{}
	}
	(*m)[key]++
}
