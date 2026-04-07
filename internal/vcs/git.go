package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type DiffSummary struct {
	ChangedPaths []string
	ChangedLines int
	Stats        []DiffStat
}

type DiffStat struct {
	Path    string
	Added   int
	Deleted int
}

// IsGitRepo returns true if root (or an ancestor) contains a .git directory.
func IsGitRepo(root string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = root
	cmd.Stderr = nil
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// HeadCommit returns the current HEAD commit hash for the repo at root.
func HeadCommit(root string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// HasUncommittedChanges returns true if there are staged, unstaged, or
// untracked-but-not-ignored changes that match any of the given extensions.
// If extensions is nil, all files are considered.
func HasUncommittedChanges(root string, extensions []string) (bool, error) {
	// Check both tracked changes and untracked (non-ignored) files.
	cmd := exec.Command("git", "status", "--porcelain", "-uall")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if len(line) < 4 {
			continue
		}
		file := strings.TrimSpace(line[3:])
		if matchesExtensions(file, extensions) {
			return true, nil
		}
	}
	return false, nil
}

// ListFiles returns sorted relative paths of tracked files (plus untracked-
// but-not-ignored files) whose extension is in the given set.
// Paths use forward slashes.
func ListFiles(root string, extensions []string) ([]string, error) {
	// --cached: tracked files. --others --exclude-standard: untracked but not ignored.
	cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	lines := strings.Split(raw, "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rel := filepath.ToSlash(line)
		if matchesExtensions(rel, extensions) {
			files = append(files, rel)
		}
	}
	sort.Strings(files)
	return files, nil
}

// UntrackedFiles returns sorted relative paths for untracked files that are
// not ignored by git.
func UntrackedFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	lines := strings.Split(raw, "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		files = append(files, filepath.ToSlash(line))
	}
	sort.Strings(files)
	return files, nil
}

// DiffSummarySince returns changed paths and total changed lines between the
// given commit-ish reference and the current working tree. It includes staged
// and unstaged tracked changes; untracked files are excluded.
func DiffSummarySince(root string, fromRef string) (DiffSummary, error) {
	nameStatusCmd := exec.Command("git", "-c", "core.quotepath=false", "diff", "--name-status", "--find-renames", fromRef)
	nameStatusCmd.Dir = root
	nameStatusOut, err := nameStatusCmd.Output()
	if err != nil {
		return DiffSummary{}, err
	}

	pathSet := map[string]struct{}{}
	nameStatusRaw := strings.TrimSpace(string(nameStatusOut))
	if nameStatusRaw != "" {
		for _, line := range strings.Split(nameStatusRaw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) < 2 {
				continue
			}
			status := strings.TrimSpace(parts[0])
			if status == "" {
				continue
			}
			code := status[0]
			switch code {
			case 'R', 'C':
				if len(parts) >= 3 {
					pathSet[filepath.ToSlash(strings.TrimSpace(parts[1]))] = struct{}{}
					pathSet[filepath.ToSlash(strings.TrimSpace(parts[2]))] = struct{}{}
				}
			default:
				pathSet[filepath.ToSlash(strings.TrimSpace(parts[1]))] = struct{}{}
			}
		}
	}

	changedPaths := make([]string, 0, len(pathSet))
	for path := range pathSet {
		if path == "" {
			continue
		}
		changedPaths = append(changedPaths, path)
	}
	sort.Strings(changedPaths)

	numstatCmd := exec.Command("git", "-c", "core.quotepath=false", "diff", "--numstat", "--find-renames", fromRef)
	numstatCmd.Dir = root
	numstatOut, err := numstatCmd.Output()
	if err != nil {
		return DiffSummary{}, err
	}

	changedLines := 0
	stats := make([]DiffStat, 0)
	numstatRaw := strings.TrimSpace(string(numstatOut))
	if numstatRaw != "" {
		for _, line := range strings.Split(numstatRaw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) < 2 {
				continue
			}
			added := 0
			deleted := 0
			if parts[0] != "-" {
				if parsed, parseErr := strconv.Atoi(parts[0]); parseErr == nil {
					added = parsed
				}
			}
			if parts[1] != "-" {
				if parsed, parseErr := strconv.Atoi(parts[1]); parseErr == nil {
					deleted = parsed
				}
			}
			changedLines += added + deleted
			path := ""
			if len(parts) == 3 {
				path = filepath.ToSlash(strings.TrimSpace(parts[2]))
			}
			stats = append(stats, DiffStat{
				Path:    path,
				Added:   added,
				Deleted: deleted,
			})
		}
	}

	return DiffSummary{
		ChangedPaths: changedPaths,
		ChangedLines: changedLines,
		Stats:        stats,
	}, nil
}

// WalkFiles returns sorted relative paths of files under root whose extension
// is in the given set, skipping the provided directory names.
// This is the non-git fallback that all adapters previously used.
func WalkFiles(root string, extensions []string, skipDirs []string) ([]string, error) {
	skipSet := make(map[string]struct{}, len(skipDirs))
	for _, d := range skipDirs {
		skipSet[d] = struct{}{}
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if _, ok := skipSet[d.Name()]; ok && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if matchesExtensions(path, extensions) {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// SourceFiles lists source files for a project. It auto-detects git and uses
// git ls-files when available, falling back to WalkFiles with the given
// skip directories. A .waveignore file in root is always honoured.
func SourceFiles(root string, extensions []string, fallbackSkipDirs []string) ([]string, error) {
	rules := loadWaveignore(root)

	var files []string
	var err error
	if IsGitRepo(root) {
		files, err = ListFiles(root, extensions)
	} else {
		files, err = WalkFiles(root, extensions, fallbackSkipDirs)
	}
	if err != nil {
		return nil, err
	}
	files = filterSkippedDirs(files, fallbackSkipDirs)
	return filterIgnored(files, rules), nil
}

func matchesExtensions(path string, extensions []string) bool {
	if len(extensions) == 0 {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range extensions {
		if ext == e {
			return true
		}
	}
	return false
}

func filterSkippedDirs(files []string, skipDirs []string) []string {
	if len(skipDirs) == 0 || len(files) == 0 {
		return files
	}
	skipSet := make(map[string]struct{}, len(skipDirs))
	for _, dir := range skipDirs {
		if dir == "" {
			continue
		}
		skipSet[dir] = struct{}{}
	}
	if len(skipSet) == 0 {
		return files
	}

	filtered := make([]string, 0, len(files))
	for _, file := range files {
		parts := strings.Split(filepath.ToSlash(file), "/")
		skip := false
		for _, part := range parts[:len(parts)-1] {
			if _, ok := skipSet[part]; ok {
				skip = true
				break
			}
		}
		if !skip {
			filtered = append(filtered, file)
		}
	}
	return filtered
}
