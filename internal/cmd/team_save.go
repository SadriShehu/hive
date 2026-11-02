package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/adapters"
	"github.com/sadrishehu/hive/internal/paths"
	"github.com/sadrishehu/hive/internal/store"
	"github.com/sadrishehu/hive/internal/team"
)

// savedTeam and savedMember are the shape a [[team]] template is written in,
// the same one hive team new --from reads.
type savedTeam struct {
	Name      string        `toml:"name"`
	Goal      string        `toml:"goal,omitempty"`
	Lead      string        `toml:"lead,omitempty"`
	Cwd       string        `toml:"cwd,omitempty"`
	Worktrees bool          `toml:"worktrees,omitempty"`
	Members   []savedMember `toml:"member,omitempty"`
}

type savedMember struct {
	Role   string `toml:"role"`
	Tool   string `toml:"tool"`
	Model  string `toml:"model,omitempty"`
	Prompt string `toml:"prompt,omitempty"`
}

func newTeamSaveCmd() *cobra.Command {
	var as string
	cmd := &cobra.Command{
		Use:   "save <team>",
		Short: "Save a team as a config.toml template for the next one",
		Long: "Append a [[team]] table to config.toml with the team's goal, lead, folder,\n" +
			"worktrees and members, so `hive team new <new-name> --from <name>` starts it again.\n" +
			"Only appends: nothing in the file before the new block changes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := openTeamEnv()
			if err != nil {
				return err
			}
			defer e.st.Close()
			tm, err := e.team(args[0])
			if err != nil {
				return err
			}
			name := as
			if name == "" {
				name = tm.Name
			}
			members, err := e.st.Members(tm.ID)
			if err != nil {
				return err
			}
			path := paths.ConfigPath()
			if err := saveTeam(path, tm, members, name); err != nil {
				return err
			}
			fmt.Printf("saved %s to %s · start it with hive team new <new-name> --from %s\n", name, path, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&as, "as", "", "the template's name (default: the team's)")
	return cmd
}

// saveTeam appends one [[team]] template to the config at path, after
// checking that it parses and has no template of that name yet.
func saveTeam(path string, tm store.Team, members []store.Member, name string) error {
	if err := team.ValidName(name); err != nil {
		return err
	}
	cfg, err := adapters.LoadConfig(path)
	if err != nil {
		return err
	}
	for _, t := range cfg.Teams {
		if t.Name == name {
			return fmt.Errorf("a team template %q is already in %s", name, path)
		}
	}
	lead := "none"
	leadRole := tm.LeadRole
	for _, m := range members {
		if m.Role == leadRole {
			lead = memberTool(m)
			break
		}
	}
	block := savedTeam{Name: name, Goal: tm.Goal, Lead: lead, Cwd: tm.Cwd,
		Worktrees: tm.Workspace == store.WorkspaceWorktrees}
	for _, m := range members {
		if !m.Active() || m.Role == leadRole {
			continue
		}
		block.Members = append(block.Members, savedMember{Role: m.Role, Tool: m.Tool, Model: m.Model, Prompt: m.Prompt})
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(struct {
		Team []savedTeam `toml:"team"`
	}{[]savedTeam{block}}); err != nil {
		return err
	}
	text := buf.String()
	if text != "" && text[len(text)-1] != '\n' {
		text += "\n"
	}
	return appendTemplate(path, tm.Name, text)
}

// memberTool is a member's tool[:model], as --lead takes it.
func memberTool(m store.Member) string {
	if m.Model == "" {
		return m.Tool
	}
	return m.Tool + ":" + m.Model
}

// appendTemplate writes the block after a blank line and a dated comment,
// without rewriting what the file already holds.
func appendTemplate(path, teamName, block string) error {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	sep := ""
	if len(data) > 0 {
		if data[len(data)-1] != '\n' {
			sep = "\n"
		}
		sep += "\n"
	}
	header := sep + "# saved from team " + teamName + " on " + time.Now().Format("2006-01-02") + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(header + block); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
