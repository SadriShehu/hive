package tracker

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/usage"
)

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

const fableRequest = `{"type":"assistant","requestId":"req_1","message":{"id":"msg_1","model":"claude-fable-5-1","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test"}}],"usage":{"input_tokens":1000000,"output_tokens":100000,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}},"timestamp":"2026-09-28T08:00:05.000Z"}`

func TestSyncReadsUsageAndPricesIt(t *testing.T) {
	h := newHistory(t)
	h.claude("c1", "/src/app", 100*minute, "go test")
	appendLine(t, h.transcript("c1"), fableRequest)
	h.opencode("ses_oc", "", "/src/web", "web work", 200*minute)
	h.exec(`INSERT INTO message VALUES ('m1', 'ses_oc', ?, ?, ?)`, 200*minute+1000, 200*minute+1000,
		`{"role":"assistant","providerID":"deepseek","modelID":"v4","cost":0.5,"tokens":{"input":10,"output":5,"reasoning":0,"cache":{"read":0,"write":0}}}`)

	tr, res := h.sync(t)
	if res.UsageRead != 2 || res.UsagePending != 0 {
		t.Fatalf("result = %+v", res)
	}
	tr.Models = usage.Builtin()
	ctx := context.Background()

	claude, err := tr.Usage(ctx, get(t, tr, "claude:c1"))
	if err != nil || claude.Requests != 1 || claude.CostSource != store.CostConfig || claude.CostUSD != 15 || claude.Tools["Bash"] != 2 {
		t.Errorf("claude usage = %+v, %v", claude, err)
	}
	oc, err := tr.Usage(ctx, get(t, tr, "opencode:ses_oc"))
	if err != nil || oc.CostUSD != 0.5 || oc.CostSource != store.CostTool || oc.Requests != 1 {
		t.Errorf("opencode usage = %+v, %v", oc, err)
	}
	all, err := tr.AllUsage()
	if err != nil || len(all) != 2 || all["claude:c1"].CostSource != store.CostConfig {
		t.Errorf("AllUsage = %+v, %v", all, err)
	}

	res2, err := tr.Sync(ctx)
	if err != nil || res2.UsageRead != 0 {
		t.Errorf("second sync re-read usage: %+v, %v", res2, err)
	}

	appendLine(t, h.transcript("c1"), fableRequest[:len(fableRequest)-1]+`,"x":1}`)
	if read, _, err := tr.RefreshUsage(ctx, 0); err != nil || read != 1 {
		t.Errorf("after an appended duplicate request: read %d, %v", read, err)
	}
	if u, _ := tr.Usage(ctx, get(t, tr, "claude:c1")); u.Requests != 1 || u.Tools["Bash"] != 3 {
		t.Errorf("a repeated request id was counted twice: %+v", u)
	}

	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tr.Store.Purge("claude:c1", 1))
	if _, ok, _ := tr.Store.Usage("claude:c1"); ok {
		t.Error("usage survived the purge")
	}
}

func TestRefreshUsageStopsAtTheBudget(t *testing.T) {
	h := newHistory(t)
	h.claude("c1", "/src/app", 100*minute)
	h.claude("c2", "/src/app", 101*minute)
	tr, _ := h.sync(t)
	appendLine(t, h.transcript("c1"), fableRequest)
	appendLine(t, h.transcript("c2"), fableRequest)
	read, pending, err := tr.RefreshUsage(context.Background(), time.Nanosecond)
	if err != nil || read+pending != 2 || pending == 0 {
		t.Errorf("read %d pending %d, %v", read, pending, err)
	}
	read, pending, err = tr.RefreshUsage(context.Background(), 0)
	if err != nil || pending != 0 || read == 0 {
		t.Errorf("drain: read %d pending %d, %v", read, pending, err)
	}
}
