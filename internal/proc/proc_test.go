package proc

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	out := []byte(`    1     0 3-02:00:05 /sbin/launchd
 4074  3858    05:30 claude --resume abc
 2842  2548 01:00:00 node /opt/homebrew/bin/claude -p hi
 bad line
`)
	tab := Parse(out, now)
	if len(tab) != 3 {
		t.Fatalf("parsed %d processes, want 3", len(tab))
	}
	c := tab[4074]
	if c.PPID != 3858 || c.Name() != "claude" || c.Started != now.Add(-330*time.Second).UnixMilli() {
		t.Errorf("4074 = %+v", c)
	}
	if n := tab[2842]; n.Name() != "claude" || len(n.Argv()) != 3 || n.Started != now.Add(-time.Hour).UnixMilli() {
		t.Errorf("2842 = %+v (argv %v)", n, n.Argv())
	}
	if l := tab[1]; l.Started != now.Add(-(3*24*time.Hour + 2*time.Hour + 5*time.Second)).UnixMilli() {
		t.Errorf("launchd started %v", time.UnixMilli(l.Started))
	}
}
