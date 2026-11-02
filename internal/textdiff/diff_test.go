package textdiff

import (
	"strings"
	"testing"
)

func TestUnifiedOfAnEmptyFileIsAllAdditions(t *testing.T) {
	got := Unified("a/x", "b/x", nil, []byte("one\ntwo\n"), 3)
	want := "--- a/x\n+++ b/x\n@@ -0,0 +1,2 @@\n+one\n+two\n"
	if got != want {
		t.Fatalf("diff =\n%q\nwant\n%q", got, want)
	}
}

func TestUnifiedOfAnEmptyNewFileIsAllRemovals(t *testing.T) {
	got := Unified("a/x", "b/x", []byte("one\ntwo\n"), nil, 3)
	want := "--- a/x\n+++ b/x\n@@ -1,2 +0,0 @@\n-one\n-two\n"
	if got != want {
		t.Fatalf("diff =\n%q\nwant\n%q", got, want)
	}
}

func TestUnifiedShowsAChangeInTheMiddleWithContext(t *testing.T) {
	old := []byte("one\ntwo\nthree\nfour\nfive\nsix\nseven\n")
	new := []byte("one\ntwo\nthree\nFOUR\nfive\nsix\nseven\n")
	got := Unified("a/x", "b/x", old, new, 1)
	want := "--- a/x\n+++ b/x\n@@ -3,3 +3,3 @@\n three\n-four\n+FOUR\n five\n"
	if got != want {
		t.Fatalf("diff =\n%q\nwant\n%q", got, want)
	}
}

func TestUnifiedSaysWhenTheLastLineHasNoNewline(t *testing.T) {
	got := Unified("a/x", "b/x", []byte("one\n"), []byte("one"), 3)
	if !strings.Contains(got, "-one\n+one\n\\ No newline at end of file\n") {
		t.Fatalf("diff =\n%s", got)
	}
}

func TestUnifiedReturnsNothingForTheSameText(t *testing.T) {
	if got := Unified("a/x", "b/x", []byte("same\n"), []byte("same\n"), 3); got != "" {
		t.Fatalf("diff = %q, want nothing", got)
	}
}
