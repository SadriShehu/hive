package cmd

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(runWithoutTheUsersClaudeCodeFiles(m))
}

func runWithoutTheUsersClaudeCodeFiles(m *testing.M) int {
	emptyConfigDir, err := os.MkdirTemp("", "hive-claude-config-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(emptyConfigDir)
	os.Setenv("CLAUDE_CONFIG_DIR", emptyConfigDir)
	return m.Run()
}
