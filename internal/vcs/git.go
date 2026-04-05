package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

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
