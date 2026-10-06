package opencode

import (
	"context"
	"strings"

	"github.com/sadrishehu/hive/internal/agent"
)

// ListModels asks opencode what it can run right now: `opencode models`
// prints one provider/model per line for every provider with credentials.
func (a *Adapter) ListModels(ctx context.Context) ([]string, error) {
	out, err := agent.RunTool(ctx, nil, "opencode", "models")
	if err != nil {
		return nil, err
	}
	var models []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "/") && !strings.ContainsAny(line, " \t") {
			models = append(models, line)
		}
	}
	return models, nil
}
