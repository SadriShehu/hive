// Package paths locates hive's own files.
package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// Home is the user's home directory.
func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// Expand turns a leading ~ in p into the home directory.
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return filepath.Join(Home(), p[1:])
	}
	return p
}

// DataDir holds the database and log. HIVE_HOME overrides it, which keeps
// tests and experiments away from the real database.
func DataDir() string {
	if d := os.Getenv("HIVE_HOME"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "hive")
	}
	return filepath.Join(Home(), ".local", "share", "hive")
}

// DBPath is the session database.
func DBPath() string { return filepath.Join(DataDir(), "hive.db") }

// LogPath receives errors from hook invocations, which must never print.
func LogPath() string { return filepath.Join(DataDir(), "hive.log") }

// ConfigPath is the optional config file that adds tools or changes built-in
// ones. HIVE_CONFIG overrides it.
func ConfigPath() string {
	if p := os.Getenv("HIVE_CONFIG"); p != "" {
		return p
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "hive", "config.toml")
	}
	return filepath.Join(Home(), ".config", "hive", "config.toml")
}
