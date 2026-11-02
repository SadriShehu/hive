package paths

import (
	"path/filepath"
	"testing"
)

func TestTeamDirIsUnderTheDataFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HIVE_HOME", home)
	if got, want := TeamDir("api"), filepath.Join(home, "teams", "api"); got != want {
		t.Fatalf("TeamDir = %q, want %q", got, want)
	}
	if got, want := TeamFilesDir("api"), filepath.Join(home, "teams", "api", "files"); got != want {
		t.Fatalf("TeamFilesDir = %q, want %q", got, want)
	}
}
