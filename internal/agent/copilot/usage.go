package copilot

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tidwall/gjson"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

type usageCursor struct {
	agent.FileCursor
	Model   string           `json:"model,omitempty"`
	RunMsgs int              `json:"run_msgs,omitempty"`
	RunOut  map[string]int64 `json:"run_out,omitempty"`
}

func (a *Adapter) ReadUsage(ctx context.Context, s store.Session, prev store.Usage, cursor string) (store.Usage, string, error) {
	path := s.Transcript
	if path == "" {
		if s.NativeID == "" {
			return prev, cursor, agent.ErrNoUsage
		}
		path = filepath.Join(a.sessionStateDir(), s.NativeID, "events.jsonl")
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
	x.u.Partial = x.c.RunMsgs > 0
	return x.u, agent.EncodeCursor(x.c), nil
}

type usageScan struct {
	u store.Usage
	c usageCursor
}

func (x *usageScan) line(raw []byte) {
	data := gjson.GetBytes(raw, "data")
	switch gjson.GetBytes(raw, "type").String() {
	case "session.start":
		x.setModel(data.Get("selectedModel").String())
	case "session.model_change":
		x.setModel(data.Get("newModel").String())
	case "assistant.message":
		x.provisional(data.Get("outputTokens").Int())
	case "tool.execution_start":
		count(&x.u.Tools, data.Get("toolName").String())
	case "skill.invoked":
		count(&x.u.Skills, firstNonEmpty(data.Get("name").String(), data.Get("skillName").String(), data.Get("skill").String()))
	case "session.compaction_complete":
		x.u.Compactions++
	case "session.shutdown":
		x.shutdown(data)
	}
}

func (x *usageScan) setModel(model string) {
	if model != "" {
		x.c.Model = model
	}
}

func (x *usageScan) provisional(output int64) {
	x.c.RunMsgs++
	if x.c.RunOut == nil {
		x.c.RunOut = map[string]int64{}
	}
	x.c.RunOut[x.c.Model] += output
	x.u.Requests++
	x.addTokens(x.c.Model, store.Tokens{Output: output})
}

func (x *usageScan) shutdown(data gjson.Result) {
	x.u.Requests -= x.c.RunMsgs
	for model, output := range x.c.RunOut {
		x.addTokens(model, store.Tokens{Output: -output})
	}
	x.c.RunMsgs, x.c.RunOut = 0, nil
	data.Get("modelMetrics").ForEach(func(model, metrics gjson.Result) bool {
		usage := metrics.Get("usage")
		read, write := usage.Get("cacheReadTokens").Int(), usage.Get("cacheWriteTokens").Int()
		x.addTokens(model.String(), store.Tokens{
			Input:      max(0, usage.Get("inputTokens").Int()-read-write),
			Output:     usage.Get("outputTokens").Int(),
			CacheRead:  read,
			CacheWrite: write,
			Reasoning:  usage.Get("reasoningTokens").Int(),
		})
		x.u.Requests += int(metrics.Get("requests.count").Int())
		return true
	})
	x.u.PremiumRequests += data.Get("totalPremiumRequests").Float()
	x.u.APIDurationMS += data.Get("totalApiDurationMs").Int()
	x.u.LinesAdded += int(data.Get("codeChanges.linesAdded").Int())
	x.u.LinesRemoved += int(data.Get("codeChanges.linesRemoved").Int())
	if tokens := data.Get("currentTokens").Int(); tokens > 0 {
		x.u.ContextTokens = tokens
	}
	x.setModel(data.Get("currentModel").String())
}

func (x *usageScan) addTokens(model string, t store.Tokens) {
	x.u.Tokens = x.u.Tokens.Add(t)
	if model == "" {
		return
	}
	if x.u.Models == nil {
		x.u.Models = map[string]store.Tokens{}
	}
	sum := x.u.Models[model].Add(t)
	if sum.IsZero() {
		delete(x.u.Models, model)
		return
	}
	x.u.Models[model] = sum
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
