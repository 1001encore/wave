package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	ChangedLines int     `json:"changed_lines"`
	TotalLines   int     `json:"total_lines"`
	LineRatio    float64 `json:"changed_line_ratio"`
}

func ComputeFreshness(ctx context.Context, st *store.Store, adapter Adapter, unit workspace.Unit) (Freshness, error) {
	// Preferred path: if this is a git repo with a stored commit hash, compute
	// freshness from git diff against the indexed commit.
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

	indexedFiles, err := st.IndexedFiles(ctx, unit.RootPath, adapter.ID())
	if err != nil {
		return Freshness{}, false, err
	}

	currentPaths, err := adapter.SourceFiles(unit)
	if err != nil {
		return Freshness{}, false, fmt.Errorf("list source files: %w", err)
	}
	currentSet := make(map[string]struct{}, len(currentPaths))
	lineCountByPath := make(map[string]int, len(currentPaths))
	totalLines := 0
	for _, path := range currentPaths {
		currentSet[path] = struct{}{}
		lineCount, lineErr := countFileLines(filepath.Join(unit.RootPath, filepath.FromSlash(path)))
		if lineErr != nil {
			continue
		}
		lineCountByPath[path] = lineCount
		totalLines += lineCount
	}

	diffSummary, err := vcs.DiffSummarySince(unit.RootPath, storedHash)
	if err != nil {
		return Freshness{}, false, err
	}

	untracked, err := vcs.UntrackedFiles(unit.RootPath)
	if err != nil {
		return Freshness{}, false, err
	}

	changedPathSet := make(map[string]struct{}, len(diffSummary.ChangedPaths)+len(untracked))
	untrackedSet := make(map[string]struct{}, len(untracked))
	for _, path := range diffSummary.ChangedPaths {
		normalized := strings.TrimSpace(filepath.ToSlash(path))
		if normalized == "" {
			continue
		}
		changedPathSet[normalized] = struct{}{}
	}
	for _, path := range untracked {
		normalized := strings.TrimSpace(filepath.ToSlash(path))
		if normalized == "" {
			continue
		}
		untrackedSet[normalized] = struct{}{}
		changedPathSet[normalized] = struct{}{}
	}

	result := Freshness{
		Status:       "clean",
		IndexedFiles: len(indexedFiles),
		CurrentFiles: len(currentPaths),
		TotalLines:   totalLines,
	}

	relevantChangedPaths := make(map[string]struct{}, len(changedPathSet))
	for path := range changedPathSet {
		_, inCurrent := currentSet[path]
		_, inIndexed := indexedFiles[path]
		if inCurrent || inIndexed {
			relevantChangedPaths[path] = struct{}{}
		}
	}

	for _, stat := range diffSummary.Stats {
		if stat.Added == 0 && stat.Deleted == 0 {
			continue
		}
		for _, candidate := range expandDiffStatPaths(stat.Path) {
			if _, ok := relevantChangedPaths[candidate]; ok {
				result.ChangedLines += stat.Added + stat.Deleted
				break
			}
		}
	}

	for path := range relevantChangedPaths {
		_, inCurrent := currentSet[path]
		_, inIndexed := indexedFiles[path]

		switch {
		case inIndexed && !inCurrent:
			result.MissingFiles++
		case !inIndexed && inCurrent:
			result.NewFiles++
			// Untracked files are not included in git diff --numstat.
			if _, isUntracked := untrackedSet[path]; isUntracked {
				result.ChangedLines += lineCountByPath[path]
			}
		default:
			result.DirtyFiles++
		}
	}

	result.ChangedFiles = result.DirtyFiles + result.NewFiles + result.MissingFiles
	denominator := result.IndexedFiles
	if result.CurrentFiles > denominator {
		denominator = result.CurrentFiles
	}
	if denominator > 0 {
		result.ChangedRatio = float64(result.ChangedFiles) / float64(denominator)
	}
	result.LineRatio = changedLineRatio(result.ChangedLines, result.TotalLines)
	result.Dirty = result.LineRatio > 0
	if result.Dirty {
		result.Status = "stale"
	}

	return result, true, nil
}

func countFileLines(path string) (int, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return countContentLines(content), nil
}

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
		content, readErr := os.ReadFile(filepath.Join(unit.RootPath, filepath.FromSlash(path)))
		if readErr != nil {
			result.MissingFiles++
			if indexed, ok := indexedFiles[path]; ok {
				result.ChangedLines += indexed.LineCount
			}
			continue
		}
		lineCount := countContentLines(content)
		result.TotalLines += lineCount

		indexed, ok := indexedFiles[path]
		if !ok {
			result.NewFiles++
			result.ChangedLines += lineCount
			continue
		}

		if sha256Hex(content) != indexed.ContentHash {
			result.DirtyFiles++
			result.ChangedLines += lineCount
		}
	}

	for path := range indexedFiles {
		if _, ok := currentSet[path]; !ok {
			result.MissingFiles++
			result.ChangedLines += indexedFiles[path].LineCount
		}
	}

	result.ChangedFiles = result.DirtyFiles + result.NewFiles + result.MissingFiles
	denominator := result.IndexedFiles
	if result.CurrentFiles > denominator {
		denominator = result.CurrentFiles
	}
	if denominator > 0 {
		result.ChangedRatio = float64(result.ChangedFiles) / float64(denominator)
	}
	result.LineRatio = changedLineRatio(result.ChangedLines, result.TotalLines)
	result.Dirty = result.LineRatio > 0
	if result.Dirty {
		result.Status = "stale"
	}
	return result, nil
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func countContentLines(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	lines := 0
	for _, b := range content {
		if b == '\n' {
			lines++
		}
	}
	if content[len(content)-1] != '\n' {
		lines++
	}
	return lines
}

func changedLineRatio(changedLines int, totalLines int) float64 {
	if changedLines <= 0 {
		return 0
	}
	if totalLines <= 0 {
		return 1
	}
	ratio := float64(changedLines) / float64(totalLines)
	if ratio > 1 {
		return 1
	}
	return ratio
}

func expandDiffStatPaths(path string) []string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return nil
	}
	if !strings.Contains(path, "=>") {
		return []string{path}
	}

	if strings.Contains(path, "{") && strings.Contains(path, "}") {
		left := strings.Index(path, "{")
		right := strings.LastIndex(path, "}")
		if left >= 0 && right > left {
			prefix := path[:left]
			suffix := path[right+1:]
			middle := path[left+1 : right]
			parts := strings.SplitN(middle, "=>", 2)
			if len(parts) == 2 {
				oldPath := filepath.ToSlash(strings.TrimSpace(prefix + strings.TrimSpace(parts[0]) + suffix))
				newPath := filepath.ToSlash(strings.TrimSpace(prefix + strings.TrimSpace(parts[1]) + suffix))
				var out []string
				if oldPath != "" {
					out = append(out, oldPath)
				}
				if newPath != "" && newPath != oldPath {
					out = append(out, newPath)
				}
				if len(out) > 0 {
					return out
				}
			}
		}
	}

	parts := strings.SplitN(path, "=>", 2)
	if len(parts) == 2 {
		oldPath := filepath.ToSlash(strings.TrimSpace(parts[0]))
		newPath := filepath.ToSlash(strings.TrimSpace(parts[1]))
		var out []string
		if oldPath != "" {
			out = append(out, oldPath)
		}
		if newPath != "" && newPath != oldPath {
			out = append(out, newPath)
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{path}
}
