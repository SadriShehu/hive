package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCatalog(t *testing.T, dir, fileName, surface string, fetchedAt int64, models ...string) {
	t.Helper()
	var entries []string
	for _, m := range models {
		id, section, _ := strings.Cut(m, "@")
		entries = append(entries, fmt.Sprintf(`{"id":%q,"section":%q}`, id, section))
	}
	body := fmt.Sprintf(`{"version":2,"fetchedAt":%d,"catalog":{"surface":%q,"config":{"models":[%s]}}}`,
		fetchedAt, surface, strings.Join(entries, ","))
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func specModels(dir string) string {
	return strings.Join((&Adapter{CatalogDir: dir}).Spec().Models, " ")
}

func TestSpecModelsAreTheMainSectionOfTheNewestClaudeCodeCatalog(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir, "account-old-cc.json", "cc", 100, "claude-opus-5-5@main", "claude-opus-5@overflow")
	writeCatalog(t, dir, "account-new-cc.json", "cc", 200, "claude-opus-6@main", "claude-fable-6@main", "claude-opus-5-5@overflow")
	writeCatalog(t, dir, "account-desktop-ccd.json", "ccd", 300, "claude-desktop-only@main")
	writeCatalog(t, dir, "misnamed-cc.json", "ccd", 400, "claude-other-surface@main")
	if got := specModels(dir); got != "claude-opus-6 claude-fable-6" {
		t.Errorf("models = %q, want the main section of the newest cc catalog, in its order", got)
	}
}

func TestSpecModelsFallBackToTheShippedListWithoutAUsableCatalog(t *testing.T) {
	shipped := strings.Join(modelsShippedWithHive, " ")
	missing := filepath.Join(t.TempDir(), "nowhere")
	if got := specModels(missing); got != shipped {
		t.Errorf("no catalog folder: models = %q, want %q", got, shipped)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken-cc.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeCatalog(t, dir, "no-main-cc.json", "cc", 500, "claude-opus-5@overflow")
	if got := specModels(dir); got != shipped {
		t.Errorf("unusable catalogs: models = %q, want %q", got, shipped)
	}
}

func TestABrokenNewerCatalogDoesNotHideAnOlderGoodOne(t *testing.T) {
	dir := t.TempDir()
	writeCatalog(t, dir, "good-cc.json", "cc", 100, "claude-opus-6@main")
	writeCatalog(t, dir, "empty-cc.json", "cc", 900)
	if got := specModels(dir); got != "claude-opus-6" {
		t.Errorf("models = %q, want claude-opus-6", got)
	}
}

func TestTheCatalogFolderFollowsClaudeConfigDir(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	dir := filepath.Join(config, "cache", "model-catalog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCatalog(t, dir, "account-cc.json", "cc", 100, "claude-opus-6@main")
	if got := strings.Join(New().Spec().Models, " "); got != "claude-opus-6" {
		t.Errorf("models = %q, want claude-opus-6", got)
	}
}
