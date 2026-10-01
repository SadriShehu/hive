package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanLinesResumesAndWaitsForWholeLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.jsonl")
	if err := os.WriteFile(path, []byte("a\nb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []string
	collect := func(l []byte) { got = append(got, string(bytes.TrimSpace(l))) }
	ctx := context.Background()

	c, err := ScanLines(ctx, path, 0, collect)
	if err != nil || len(got) != 2 || got[1] != "b" || c.Offset != 4 {
		t.Fatalf("first scan: %v %v %+v", got, err, c)
	}
	fi, _ := os.Stat(path)
	if !c.Unchanged(fi) || c.Behind(fi) {
		t.Errorf("cursor %+v should match the unchanged file", c)
	}

	appendTo(t, path, "c")
	got = nil
	c2, err := ScanLines(ctx, path, c.Offset, collect)
	if err != nil || len(got) != 0 || c2.Offset != 4 {
		t.Fatalf("an unterminated line was consumed: %v %+v", got, c2)
	}

	appendTo(t, path, "\nd\n")
	got = nil
	c3, err := ScanLines(ctx, path, c2.Offset, collect)
	if err != nil || len(got) != 2 || got[0] != "c" || got[1] != "d" || c3.Offset != 8 {
		t.Fatalf("resume: %v %v %+v", got, err, c3)
	}

	if err := os.WriteFile(path, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, _ = os.Stat(path)
	if !c3.Behind(fi) {
		t.Error("a truncated file should report Behind")
	}
}

func TestCursorCodec(t *testing.T) {
	type cursor struct {
		FileCursor
		Last string `json:"last"`
	}
	in := cursor{FileCursor{Offset: 1, Size: 2, MTime: 3}, "req"}
	var out cursor
	if !DecodeCursor(EncodeCursor(in), &out) || out != in {
		t.Errorf("round trip = %+v", out)
	}
	if DecodeCursor("", &out) || DecodeCursor("{bad", &out) {
		t.Error("an empty or broken cursor decoded")
	}
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}
