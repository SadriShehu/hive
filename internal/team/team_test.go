package team

import (
	"strings"
	"testing"
	"unicode"

	"github.com/sadrishehu/hive/internal/store"
)

func TestParseSpec(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Spec
		bad  bool
	}{
		{"backend=claude", Spec{Role: "backend", Tool: "claude"}, false},
		{"backend=claude:auto", Spec{Role: "backend", Tool: "claude", Model: "auto"}, false},
		{"reviewer=opencode:deepseek/v4", Spec{Role: "reviewer", Tool: "opencode", Model: "deepseek/v4"}, false},
		{"backend", Spec{}, true},
		{"backend=", Spec{}, true},
		{"=claude", Spec{}, true},
		{"all=claude", Spec{}, true}, // addresses everyone
		{"you=claude", Spec{}, true},
		{"Back End=claude", Spec{}, true},
	} {
		got, err := ParseSpec(c.in)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("ParseSpec(%q) = %+v, %v; want %+v (error %v)", c.in, got, err, c.want, c.bad)
		}
	}
	if err := CheckRoles([]Spec{{Role: "a"}, {Role: "a"}}); err == nil {
		t.Error("CheckRoles accepted a role twice")
	}
}

func TestEnvelopeKeepsEveryLineOutOfTheTUIsCommands(t *testing.T) {
	for _, body := range []string{"/exit", "!rm -rf /", "# remember this", "two\nlines\n/exit", "  /exit"} {
		got := Envelope("api", "backend", body)
		if !strings.HasPrefix(got, "[api · from backend] ") || strings.Contains(got, "\n") {
			t.Errorf("Envelope(%q) = %q, want one line starting with the envelope", body, got)
		}
	}
	paste := Paste("api", []store.Message{{From: "lead", Body: "a\nb"}, {From: "hive", Body: "c"}})
	for line := range strings.SplitSeq(paste, "\n") {
		if !strings.HasPrefix(line, "[") {
			t.Errorf("line %q of %q doesn't start with [", line, paste)
		}
	}
	if n := strings.Count(paste, "\n"); n != 1 {
		t.Errorf("two messages paste as %d lines", n+1)
	}
}

func TestEnvelopeHoldsNoControlCharacter(t *testing.T) {
	// The end of a bracketed paste, then Ctrl-C and Esc Esc as keys.
	got := Envelope("api", "lead\x1b", "stop\x1b[201~\x03\x1b\x1b\u009b rewind\tnow")
	if strings.ContainsFunc(got, unicode.IsControl) {
		t.Errorf("Envelope = %q, want no control characters", got)
	}
	if !strings.HasPrefix(got, "[api · from lead] stop [201~") || !strings.HasSuffix(got, "rewind now") {
		t.Errorf("Envelope = %q, want the text kept around the spaces", got)
	}
}

func TestDecide(t *testing.T) {
	for _, c := range []struct {
		paused bool
		status string
		inPane bool
		want   Delivery
	}{
		{false, store.StatusIdle, true, Type},
		{false, store.StatusIdle, false, Queue},
		{false, store.StatusWorking, true, Queue},
		{false, store.StatusAttention, true, Queue}, // a prompt would take it as an answer
		{false, store.StatusUnknown, true, Blind},
		{false, store.StatusUnknown, false, Queue},
		{false, store.StatusExited, false, Reopen},
		{true, store.StatusIdle, true, Queue},
		{true, store.StatusExited, false, Queue},
	} {
		if got := Decide(c.paused, c.status, c.inPane); got != c.want {
			t.Errorf("Decide(paused=%v, %s, inPane=%v) = %v, want %v", c.paused, c.status, c.inPane, got, c.want)
		}
	}
}

func TestRecipientsAndAddress(t *testing.T) {
	members := []store.Member{{Role: "lead"}, {Role: "backend"}, {Role: "old", LeftAt: 1}}
	got, err := Recipients("all", "lead", members)
	if err != nil || strings.Join(got, ",") != "backend" {
		t.Errorf("all from lead = %v, %v; want backend only (not the sender, not the retired)", got, err)
	}
	if _, err := Recipients("lead", "lead", members); err == nil {
		t.Error("lead messaged itself")
	}
	if _, err := Recipients("old", "lead", members); err == nil {
		t.Error("messaged a retired member")
	}

	for _, c := range []struct{ in, to, body string }{
		{"@backend add the endpoint", "backend", "add the endpoint"},
		{"@all stand up", "all", "stand up"},
		{"just text", "all", "just text"},
		{"@ lonely", "all", "@ lonely"},
	} {
		if to, body := Address(c.in); to != c.to || body != c.body {
			t.Errorf("Address(%q) = %q, %q; want %q, %q", c.in, to, body, c.to, c.body)
		}
	}
	if got := Mentions("ask @backend, not @back or me@lead.com", members); strings.Join(got, ",") != "backend" {
		t.Errorf("Mentions = %v, want backend only", got)
	}
	ops := []store.Member{{Role: "dev"}, {Role: "dev-ops"}, {Role: "dev_2"}}
	for body, want := range map[string]string{
		"@dev-ops please deploy": "dev-ops",
		"@dev_2 and @dev.":       "dev,dev_2",
		"@dev":                   "dev",
	} {
		if got := strings.Join(Mentions(body, ops), ","); got != want {
			t.Errorf("Mentions(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestBriefSaysWhoLeads(t *testing.T) {
	members := []store.Member{{Role: "lead", Tool: "claude"}, {Role: "backend", Tool: "codex"}}
	tm := store.Team{Name: "api", Goal: "ship", Cwd: "/work", LeadRole: "lead"}
	if b := Brief(tm, members[0], members); !strings.Contains(b, "You lead.") || !strings.Contains(b, "Goal: ship") {
		t.Errorf("lead brief = %q", b)
	}
	b := Brief(tm, store.Member{Role: "backend", Prompt: "own internal/"}, members)
	for _, want := range []string{"You are backend", "Your part: own internal/", "hive team post", "[api · from <role>]", "The lead is lead"} {
		if !strings.Contains(b, want) {
			t.Errorf("member brief lacks %q:\n%s", want, b)
		}
	}
	tm.LeadRole = ""
	for _, m := range members { // even the member whose role is called lead
		if b := Brief(tm, m, members); !strings.Contains(b, "The human leads") || strings.Contains(b, "You lead") {
			t.Errorf("%s's brief with the user leading = %q", m.Role, b)
		}
	}
}
