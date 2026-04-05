package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/1001encore/wave/internal/store"
	"github.com/1001encore/wave/internal/vcs"
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
	// Fast path: if this is a git repo with a stored commit hash, compare
	// HEAD against the indexed commit. If HEAD matches and there are no
	// uncommitted changes to relevant files, the index is clean.
	if vcs.IsGitRepo(unit.RootPath) {
		result, done, err := gitFreshness(ctx, st, adapter, unit)
		if err == nil && done {
			return result, nil
		}
		// Fall through to full file-level check on any error.
	}

	return fullFreshness(ctx, st, adapter, unit)
}

// gitFreshness uses the stored git commit hash to quickly determine freshness.
// It returns (result, true, nil) when it can conclusively answer, or
// (Freshness{}, false, err) when the caller should fall back to full scanning.
func gitFreshness(ctx context.Context, st *store.Store, adapter Adapter, unit workspace.Unit) (Freshness, bool, error) {
	storedHash, err := st.GitCommitHash(ctx, unit.RootPath, adapter.ID())
	if err != nil || storedHash == "" {
		return Freshness{}, false, fmt.Errorf("no stored commit hash")
	}

	currentHash, err := vcs.HeadCommit(unit.RootPath)
	if err != nil {
		return Freshness{}, false, err
	}

	if currentHash != storedHash {
		// HEAD moved — we can't tell how many files changed without a full
		// scan, but we know the index is stale. Return a high change count
		// so the auto-reindex heuristic fires.
		return Freshness{
			Status:       "stale",
			Dirty:        true,
			ChangedFiles: autoReindexFallbackChangedFiles,
			ChangedRatio: 1.0,
		}, true, nil
	}

	// HEAD hasn't moved. Check for uncommitted changes to relevant source files.
	hasChanges, err := vcs.HasUncommittedChanges(unit.RootPath, nil)
	if err != nil {
		return Freshness{}, false, err
	}
	if hasChanges {
		// There are working-tree changes. Fall back to full scan so we get
		// accurate dirty/new/missing counts.
		return Freshness{}, false, fmt.Errorf("uncommitted changes present")
	}

	return Freshness{Status: "clean"}, true, nil
}

// autoReindexFallbackChangedFiles is a synthetic count used when git detects
// that HEAD has moved. It's set high enough to always trigger auto-reindex.
const autoReindexFallbackChangedFiles = 10000

func fullFreshness(ctx context.Context, st *store.Store, adapter Adapter, unit workspace.Unit) (Freshness, error) {
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
