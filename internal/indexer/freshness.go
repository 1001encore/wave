package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/1001encore/wave/internal/store"
	"github.com/1001encore/wave/internal/workspace"
)

type Freshness struct {
	Status       string  `json:"status"`
	Dirty        bool    `json:"dirty"`
	IndexedFiles int     `json:"indexed_files"`
	CurrentFiles int     `json:"current_files"`
	DirtyFiles   int     `json:"dirty_files"`
	NewFiles     int     `json:"new_files"`
	MissingFiles int     `json:"missing_files"`
	ChangedFiles int     `json:"changed_files"`
	ChangedRatio float64 `json:"changed_ratio"`
}

func ComputeFreshness(ctx context.Context, st *store.Store, adapter Adapter, unit workspace.Unit) (Freshness, error) {
	indexedFiles, err := st.IndexedFiles(ctx, unit.RootPath, adapter.ID())
	if err != nil {
		return Freshness{}, err
	}

	currentPaths, err := adapter.SourceFiles(unit)
	if err != nil {
		return Freshness{}, fmt.Errorf("list source files: %w", err)
	}

	currentSet := make(map[string]struct{}, len(currentPaths))
	for _, path := range currentPaths {
		currentSet[path] = struct{}{}
	}

	result := Freshness{
		Status:       "clean",
		IndexedFiles: len(indexedFiles),
		CurrentFiles: len(currentPaths),
	}

	for _, path := range currentPaths {
		indexed, ok := indexedFiles[path]
		if !ok {
			result.NewFiles++
			continue
		}

		content, readErr := os.ReadFile(indexed.AbsPath)
		if readErr != nil {
			result.MissingFiles++
			continue
		}
		if sha256Hex(content) != indexed.ContentHash {
			result.DirtyFiles++
		}
	}

	for path := range indexedFiles {
		if _, ok := currentSet[path]; !ok {
			result.MissingFiles++
		}
	}

	result.Dirty = result.DirtyFiles > 0 || result.NewFiles > 0 || result.MissingFiles > 0
	if result.Dirty {
		result.Status = "stale"
	}
	result.ChangedFiles = result.DirtyFiles + result.NewFiles + result.MissingFiles
	denominator := result.IndexedFiles
	if result.CurrentFiles > denominator {
		denominator = result.CurrentFiles
	}
	if denominator > 0 {
		result.ChangedRatio = float64(result.ChangedFiles) / float64(denominator)
	}
	return result, nil
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
