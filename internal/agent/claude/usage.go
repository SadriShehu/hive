package claude

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/sadrishehu/hive/internal/agent"
	"github.com/sadrishehu/hive/internal/store"
)

const (
	defaultWindow    = 200_000
	oneMillionWindow = 1_000_000
)

type usageCursor struct {
	agent.FileCursor
	LastRequest string  `json:"req,omitempty"`
	RunStart    string  `json:"run,omitempty"`
	RunCost     float64 `json:"run_cost,omitempty"`
	RunAPI      int64   `json:"run_api,omitempty"`
	RunAdded    int     `json:"run_added,omitempty"`
	RunRemoved  int     `json:"run_removed,omitempty"`
	Uncosted    int     `json:"uncosted,omitempty"`
	Peak        int64   `json:"peak,omitempty"`
	OneMillion  bool    `json:"one_million,omitempty"`
}

func (a *Adapter) ReadUsage(ctx context.Context, s store.Session, prev store.Usage, cursor string) (store.Usage, string, error) {
	if s.Transcript == "" {
		return prev, cursor, agent.ErrNoUsage
	}
	fi, err := os.Stat(s.Transcript)
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
	x := &usageScan{u: prev, c: c, subagent: s.Kind == store.KindInternal}
	x.u.ID = s.ID
	fc, err := agent.ScanLines(ctx, s.Transcript, c.Offset, x.line)
	if err != nil {
		return prev, cursor, err
	}
	x.c.FileCursor = fc
	x.finish(a.settingsOneMillion(), s.Live())
	return x.u, agent.EncodeCursor(x.c), nil
}

func (a *Adapter) settingsOneMillion() bool {
	data, err := os.ReadFile(a.settingsPath())
	return err == nil && strings.Contains(gjson.GetBytes(data, "model").String(), "[1m]")
}

type usageScan struct {
	u        store.Usage
	c        usageCursor
	subagent bool
}

var recordTypes = [][]byte{[]byte(`"type":"assistant"`), []byte(`"type":"cost-state"`), []byte(`"type":"user"`)}

var commandName = regexp.MustCompile(`<command-name>([^<]+)</command-name>`)

func (x *usageScan) line(raw []byte) {
	if !containsAny(raw, recordTypes) {
		return
	}
	switch gjson.GetBytes(raw, "type").String() {
	case "assistant":
		x.assistant(raw)
	case "cost-state":
		x.costState(raw)
	case "user":
		x.user(raw)
	}
}

func (x *usageScan) assistant(raw []byte) {
	msg := gjson.GetBytes(raw, "message")
	model := msg.Get("model").String()
	if model == "<synthetic>" {
		return
	}
	msg.Get("content").ForEach(func(_, c gjson.Result) bool {
		if c.Get("type").String() != "tool_use" {
			return true
		}
		name := c.Get("name").String()
		count(&x.u.Tools, name)
		if skill := c.Get("input.skill").String(); name == "Skill" && skill != "" {
			count(&x.u.Skills, skill)
		}
		return true
	})
	key := firstNonEmpty(gjson.GetBytes(raw, "requestId").String(), msg.Get("id").String())
	if key == "" || key == x.c.LastRequest {
		return
	}
	x.c.LastRequest = key
	usage := msg.Get("usage")
	if !usage.Exists() {
		return
	}
	t := store.Tokens{
		Input:        usage.Get("input_tokens").Int(),
		Output:       usage.Get("output_tokens").Int(),
		CacheRead:    usage.Get("cache_read_input_tokens").Int(),
		CacheWrite:   usage.Get("cache_creation_input_tokens").Int(),
		CacheWrite1h: usage.Get("cache_creation.ephemeral_1h_input_tokens").Int(),
		Reasoning:    usage.Get("output_tokens_details.thinking_tokens").Int(),
	}
	x.u.Tokens = x.u.Tokens.Add(t)
	if model != "" {
		if x.u.Models == nil {
			x.u.Models = map[string]store.Tokens{}
		}
		x.u.Models[model] = x.u.Models[model].Add(t)
		x.u.Model = model
	}
	x.u.Requests++
	x.c.Uncosted++
	if effort := effortLevel(raw); effort != "" {
		x.u.Effort = effort
	}
	x.u.ContextTokens = t.Input + t.CacheWrite + t.CacheRead
	x.c.Peak = max(x.c.Peak, x.u.ContextTokens)
}

func (x *usageScan) costState(raw []byte) {
	gjson.GetBytes(raw, "modelUsage").ForEach(func(key, _ gjson.Result) bool {
		if strings.Contains(key.String(), "[1m]") {
			x.c.OneMillion = true
		}
		return true
	})
	if x.subagent {
		return
	}
	start := gjson.GetBytes(raw, "startTime").Raw
	cost := gjson.GetBytes(raw, "totalCostUSD").Float()
	api := gjson.GetBytes(raw, "totalAPIDuration").Int()
	added := int(gjson.GetBytes(raw, "totalLinesAdded").Int())
	removed := int(gjson.GetBytes(raw, "totalLinesRemoved").Int())
	if start == x.c.RunStart && x.u.CostSource == store.CostTool {
		x.u.CostUSD -= x.c.RunCost
		x.u.APIDurationMS -= x.c.RunAPI
		x.u.LinesAdded -= x.c.RunAdded
		x.u.LinesRemoved -= x.c.RunRemoved
	}
	x.u.CostUSD += cost
	x.u.APIDurationMS += api
	x.u.LinesAdded += added
	x.u.LinesRemoved += removed
	x.u.CostSource = store.CostTool
	x.c.RunStart, x.c.RunCost, x.c.RunAPI, x.c.RunAdded, x.c.RunRemoved = start, cost, api, added, removed
	x.c.Uncosted = 0
}

func (x *usageScan) user(raw []byte) {
	if gjson.GetBytes(raw, "isCompactSummary").Bool() {
		x.u.Compactions++
	}
	text := strings.TrimSpace(firstText(gjson.GetBytes(raw, "message.content")))
	if !strings.HasPrefix(text, "<command-") {
		return
	}
	if m := commandName.FindStringSubmatch(text); m != nil {
		count(&x.u.Skills, strings.TrimSpace(m[1]))
	}
}

func (x *usageScan) finish(settingsOneMillion, live bool) {
	x.u.ContextWindow = defaultWindow
	if x.c.OneMillion || settingsOneMillion || x.c.Peak > defaultWindow {
		x.u.ContextWindow = oneMillionWindow
	}
	x.u.Partial = live && !x.subagent && x.c.Uncosted > 0
}

func effortLevel(raw []byte) string {
	for _, key := range []string{"perTurnEffort", "effort"} {
		v := gjson.GetBytes(raw, key)
		if v.IsObject() {
			v = v.Get("level")
		}
		if v.String() != "" {
			return v.String()
		}
	}
	return ""
}

func firstText(content gjson.Result) string {
	if !content.IsArray() {
		return content.String()
	}
	for _, c := range content.Array() {
		if c.Get("type").String() == "text" {
			return c.Get("text").String()
		}
	}
	return ""
}

func count(m *map[string]int, key string) {
	if *m == nil {
		*m = map[string]int{}
	}
	(*m)[key]++
}

func containsAny(raw []byte, needles [][]byte) bool {
	for _, n := range needles {
		if bytes.Contains(raw, n) {
			return true
		}
	}
	return false
}
