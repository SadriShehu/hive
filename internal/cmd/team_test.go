package cmd

import (
	"testing"

	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/tracker"
)

func TestSenderNeverPassesAnAgentOffAsTheUser(t *testing.T) {
	api, web := store.Team{ID: "t1", Name: "api"}, store.Team{ID: "t2", Name: "web"}
	backend := []tracker.Role{{Team: api, Member: store.Member{TeamID: "t1", Role: "backend"}}}
	for _, c := range []struct {
		name string
		e    teamEnv
		to   store.Team
		as   string
		want string
	}{
		{"a member, on its team", teamEnv{caller: "claude:A", roles: backend}, api, "", "backend"},
		{"a member, on another team", teamEnv{caller: "claude:A", roles: backend}, web, "", "api/backend"},
		{"an agent on no team", teamEnv{caller: "claude:C"}, api, "", "claude:C"},
		{"a shell outside any agent", teamEnv{}, api, "", store.FromYou},
		{"--as", teamEnv{caller: "claude:C"}, api, "lead", "lead"},
	} {
		if got := c.e.sender(c.to, c.as); got != c.want {
			t.Errorf("%s: sender = %q, want %q", c.name, got, c.want)
		}
	}
}
