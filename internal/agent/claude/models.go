package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	claudeCodeSurface     = "cc"
	currentModelSection   = "main"
	claudeCodeCatalogGlob = "*-" + claudeCodeSurface + ".json"
)

var modelsShippedWithHive = []string{"claude-fable-5-2", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"}

type catalogFile struct {
	FetchedAt int64 `json:"fetchedAt"`
	Catalog   struct {
		Surface string `json:"surface"`
		Config  struct {
			Models []catalogModel `json:"models"`
		} `json:"config"`
	} `json:"catalog"`
}

type catalogModel struct {
	ID      string `json:"id"`
	Section string `json:"section"`
}

func (a *Adapter) catalogDir() string {
	if a.CatalogDir != "" {
		return a.CatalogDir
	}
	return filepath.Join(configDir(), "cache", "model-catalog")
}

func (a *Adapter) currentModels() []string {
	a.readCatalogOnce.Do(func() {
		a.modelsFromCatalog = currentModelsOfNewestCatalog(a.catalogDir())
	})
	if len(a.modelsFromCatalog) == 0 {
		return modelsShippedWithHive
	}
	return a.modelsFromCatalog
}

func currentModelsOfNewestCatalog(dir string) []string {
	paths, _ := filepath.Glob(filepath.Join(dir, claudeCodeCatalogGlob))
	var newest catalogFile
	var newestModels []string
	for _, path := range paths {
		file, ok := readCatalogFile(path)
		if !ok || file.Catalog.Surface != claudeCodeSurface {
			continue
		}
		models := file.currentModels()
		if len(models) > 0 && (newestModels == nil || file.FetchedAt > newest.FetchedAt) {
			newest, newestModels = file, models
		}
	}
	return newestModels
}

func readCatalogFile(path string) (catalogFile, bool) {
	var file catalogFile
	data, err := os.ReadFile(path)
	if err != nil {
		return file, false
	}
	return file, json.Unmarshal(data, &file) == nil
}

func (f catalogFile) currentModels() []string {
	var ids []string
	for _, m := range f.Catalog.Config.Models {
		if m.ID != "" && m.Section == currentModelSection {
			ids = append(ids, m.ID)
		}
	}
	return ids
}
