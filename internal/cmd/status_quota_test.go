package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sadrishehu/hive/internal/store"
)

// Claude Code pipes its status JSON into the status line; hive keeps the
// rate limits it reports.
func TestStatusCapturesRateLimits(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	old := os.Stdin
	t.Cleanup(func() { os.Stdin = old })
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	payload := `{"rate_limits":{"five_hour":{"used_percentage":7,"resets_at":1700000000},` +
		`"seven_day":{"used_percentage":22,"resets_at":1700500000}}}`
	if _, err := w.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	w.Close()
	captureRateLimits(st)

	quotas, err := st.Quotas()
	if err != nil {
		t.Fatal(err)
	}
	if got := quotas["claude"]; got.FiveUsed != 7 || got.FiveReset != 1_700_000_000 ||
		got.SevenUsed != 22 || got.SevenReset != 1_700_500_000 || got.At == 0 {
		t.Fatalf("claude quota = %+v, want the reported windows", got)
	}
}

// Someone running `hive status` themselves (a terminal or no JSON) changes
// nothing.
func TestStatusIgnoresATerminalOrEmptyInput(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	old := os.Stdin
	t.Cleanup(func() { os.Stdin = old })
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	os.Stdin = devnull
	captureRateLimits(st)
	if quotas, _ := st.Quotas(); len(quotas) != 0 {
		t.Fatalf("a character device recorded quotas: %v", quotas)
	}
}
