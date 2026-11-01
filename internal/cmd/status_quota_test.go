package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

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

	payload := `{"rate_limits":{"five_hour":{"used_percentage":7,"resets_at":1700000000},` +
		`"seven_day":{"used_percentage":22,"resets_at":1700500000}}}`
	captureRateLimits(st, []byte(payload))

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
	if data := readStatusInput(); data != nil {
		t.Fatalf("a character device read %q, want nothing", data)
	}
	captureRateLimits(st, nil)
	if quotas, _ := st.Quotas(); len(quotas) != 0 {
		t.Fatalf("empty input recorded quotas: %v", quotas)
	}
}

// A pipe held open without any bytes must not hang the status line.
func TestStatusReadDoesNotHangOnAnOpenPipe(t *testing.T) {
	old := os.Stdin
	t.Cleanup(func() { os.Stdin = old })
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close() // the writer stays open, and writes nothing
	os.Stdin = r

	done := make(chan []byte, 1)
	go func() { done <- readStatusInput() }()
	select {
	case data := <-done:
		if data != nil {
			t.Fatalf("an open, silent pipe read %q, want nothing", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readStatusInput hung on an open, silent pipe")
	}
}

// --then hands the same JSON to the user's own status line, prints what it
// prints, and keeps its exit status.
func TestStatusThenRunsTheUsersLineWithTheSameJSON(t *testing.T) {
	dir := t.TempDir()
	got := filepath.Join(dir, "got.json")
	script := filepath.Join(dir, "line.sh")
	body := "#!/bin/sh\ncat > " + got + "\nprintf 'the user line'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `{"rate_limits":{"five_hour":{"used_percentage":7}}}`
	var out, errOut bytes.Buffer
	if code := runThen(script, []byte(payload), &out, &errOut); code != 0 {
		t.Fatalf("runThen = %d, stderr %q", code, errOut.String())
	}
	if out.String() != "the user line" {
		t.Errorf("stdout = %q, want the user's line", out.String())
	}
	if data, err := os.ReadFile(got); err != nil || string(data) != payload {
		t.Errorf("the command read %q, %v; want the same JSON", data, err)
	}
	if code := runThen("exit 3", nil, io.Discard, io.Discard); code != 3 {
		t.Errorf("a command that exits 3 = %d, want 3", code)
	}
}

// With --then, the user's status line doesn't depend on hive: even when
// hive's database can't be opened, the command still gets the JSON and its
// output and exit status are kept.
func TestStatusThenWorksWhenHiveCannotOpenItsDB(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HIVE_HOME", blocked)

	got := filepath.Join(dir, "got.json")
	script := filepath.Join(dir, "line.sh")
	body := "#!/bin/sh\ncat > " + got + "\nprintf 'the user line'\nexit 3\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	old := os.Stdin
	t.Cleanup(func() { os.Stdin = old })
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	payload := `{"rate_limits":{"five_hour":{"used_percentage":7,"resets_at":1700000000}}}`
	if _, err := w.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	w.Close()

	cmd := newStatusCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	code, err := runStatus(cmd, false, script)
	if err != nil {
		t.Fatalf("runStatus = %v, want the user's line despite the database", err)
	}
	if code != 3 {
		t.Fatalf("exit code = %d, want the command's 3", code)
	}
	if out.String() != "the user line" {
		t.Errorf("stdout = %q, want the user's line", out.String())
	}
	if data, err := os.ReadFile(got); err != nil || string(data) != payload {
		t.Errorf("the command read %q, %v; want the same JSON", data, err)
	}
}

// The whole status command, not just captureRateLimits: the JSON on stdin is
// read and its rate limits land in hive's store.
func TestStatusCommandRecordsTheRateLimitsFromStdin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HIVE_HOME", home)
	setStdin(t, `{"rate_limits":{"five_hour":{"used_percentage":7,"resets_at":1700000000},`+
		`"seven_day":{"used_percentage":22,"resets_at":1700500000}}}`)

	cmd := newStatusCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if code, err := runStatus(cmd, false, ""); err != nil || code != 0 {
		t.Fatalf("runStatus = %d, %v; want the status line", code, err)
	}

	st, err := store.Open(filepath.Join(home, "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	quotas, err := st.Quotas()
	if err != nil {
		t.Fatal(err)
	}
	if got := quotas["claude"]; got.FiveUsed != 7 || got.SevenUsed != 22 {
		t.Fatalf("stored quota = %+v, want the stdin's rate limits", got)
	}
}

// With --then and a working store, the command records the limits and hands
// the same JSON to the user's line, keeping its output.
func TestStatusThenRecordsAndHandsOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HIVE_HOME", home)
	dir := t.TempDir()
	got := filepath.Join(dir, "got.json")
	script := filepath.Join(dir, "line.sh")
	body := "#!/bin/sh\ncat > " + got + "\nprintf 'the user line'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := `{"rate_limits":{"five_hour":{"used_percentage":7,"resets_at":1700000000}}}`
	setStdin(t, payload)

	cmd := newStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	code, err := runStatus(cmd, false, script)
	if err != nil || code != 0 {
		t.Fatalf("runStatus = %d, %v", code, err)
	}
	if out.String() != "the user line" {
		t.Errorf("stdout = %q, want the user's line", out.String())
	}
	if data, err := os.ReadFile(got); err != nil || string(data) != payload {
		t.Errorf("the command read %q, %v; want the same JSON", data, err)
	}
	st, err := store.Open(filepath.Join(home, "hive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	quotas, _ := st.Quotas()
	if got := quotas["claude"]; got.FiveUsed != 7 || got.FiveReset != 1_700_000_000 {
		t.Fatalf("stored quota = %+v, want the stdin's rate limits", got)
	}
}

// setStdin replaces os.Stdin with a pipe holding text, closed at EOF.
func setStdin(t *testing.T, text string) {
	t.Helper()
	old := os.Stdin
	t.Cleanup(func() { os.Stdin = old })
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	if _, err := w.WriteString(text); err != nil {
		t.Fatal(err)
	}
	w.Close()
}
