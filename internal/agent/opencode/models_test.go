package opencode

import (
	"context"
	"strings"
	"testing"

	"github.com/sadrishehu/hive/internal/agent/agenttest"
)

func TestListModelsRunsOpencodeModels(t *testing.T) {
	log := agenttest.FakeTool(t, "opencode", `printf 'opencode/big-pickle\ndeepseek/deepseek-v4-pro\n\ngoogle-vertex/claude-opus-5-5@default\n'`)
	models, err := New().ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(models, " ") != "opencode/big-pickle deepseek/deepseek-v4-pro google-vertex/claude-opus-5-5@default" {
		t.Errorf("models = %v", models)
	}
	if log() != "models" {
		t.Errorf("ran %q", log())
	}
}
