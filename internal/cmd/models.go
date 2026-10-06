package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sadrishehu/hive/internal/fusion"
	"github.com/sadrishehu/hive/internal/usage"
)

func newModelsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "models [tool]",
		Short: "List the models each installed tool can run, with hive's ratings",
		Long: "List the models each installed tool can run, as the tool names them, with the tier\n" +
			"and ratings `hive new auto` picks by. A model without a rating is never picked; a\n" +
			"[[model]] with a tier in config.toml rates it. The list comes from the tool\n" +
			"(opencode lists its own), hive's built-in lists, and the models past sessions used.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, tr, _, err := openTracker()
			if err != nil {
				return err
			}
			defer st.Close()
			tools := tr.InstalledTools()
			if len(args) == 1 {
				tools = args[:1]
			}
			var rows []modelRow
			for _, tool := range tools {
				ids, err := tr.ToolModels(cmd.Context(), tool)
				if err != nil {
					return err
				}
				for _, id := range ids {
					rows = append(rows, newModelRow(tool, id, tr.Models))
				}
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			printModels(rows, tr.Fusion)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "JSON: one row per tool and model")
	return cmd
}

type modelRow struct {
	Tool    string         `json:"tool"`
	Model   string         `json:"model"`
	Name    string         `json:"name,omitempty"` // the catalog row it is rated as
	Tier    string         `json:"tier,omitempty"`
	Ratings map[string]int `json:"ratings,omitempty"`
	Speed   int            `json:"speed,omitempty"`
	Input   float64        `json:"input_per_million,omitempty"`
	Output  float64        `json:"output_per_million,omitempty"`
	Rated   bool           `json:"rated"`
}

func newModelRow(tool, id string, catalog usage.Catalog) modelRow {
	row := modelRow{Tool: tool, Model: id}
	m, ok := catalog.Match(id)
	if !ok || !m.Rated() {
		return row
	}
	row.Name, row.Tier, row.Rated, row.Speed = m.Name, m.Tier, true, m.SpeedRating()
	row.Input, row.Output = m.Input, m.Output
	row.Ratings = map[string]int{}
	for _, s := range usage.Signals {
		row.Ratings[string(s)] = m.Rating(s)
	}
	return row
}

func printModels(rows []modelRow, settings fusion.Settings) {
	color := useColor()
	width := 0
	for _, r := range rows {
		width = max(width, len(r.Model))
	}
	tool := ""
	for _, r := range rows {
		if r.Tool != tool {
			if tool != "" {
				fmt.Println()
			}
			tool = r.Tool
			fmt.Println(paint(tool, toolStyle(tool), color))
		}
		fmt.Printf("  %-*s  %s\n", width, r.Model, paint(ratingText(r), "2", color))
	}
	if len(rows) == 0 {
		fmt.Println("no models: no agent on PATH takes a model")
		return
	}
	fmt.Println()
	fmt.Println(paint(fmt.Sprintf("hive new auto picks by these ratings in %s mode%s; a [[model]] with a tier in config.toml rates or changes one",
		settings.Mode, modeToolText(settings)), "2", color))
}

func modeToolText(settings fusion.Settings) string {
	if settings.Mode == fusion.ModeModel && settings.Tool != "" {
		return " inside " + settings.Tool
	}
	return ""
}

func ratingText(r modelRow) string {
	if !r.Rated {
		return "unrated: never picked until a [[model]] gives it a tier"
	}
	parts := []string{fmt.Sprintf("%-8s", r.Tier)}
	for _, s := range usage.Signals {
		parts = append(parts, fmt.Sprintf("%s %d", strings.ReplaceAll(string(s), "_", " "), r.Ratings[string(s)]))
	}
	parts = append(parts, fmt.Sprintf("speed %d", r.Speed))
	if r.Input > 0 || r.Output > 0 {
		parts = append(parts, fmt.Sprintf("$%g in · $%g out per M", r.Input, r.Output))
	}
	return strings.Join(parts, " · ")
}
