package vcs

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// waveignoreFile is the name of the ignore file looked up in the project root.
const waveignoreFile = ".waveignore"

// ignoreRule is a single parsed line from a .waveignore file.
type ignoreRule struct {
	pattern string
	isDir   bool // true when the original line ended with "/"
}

// loadWaveignore reads .waveignore from root and returns parsed rules.
// Returns nil (no error) when the file does not exist.
func loadWaveignore(root string) []ignoreRule {
	path := filepath.Join(root, waveignoreFile)
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var rules []ignoreRule
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		isDir := strings.HasSuffix(line, "/")
		pattern := strings.TrimSuffix(line, "/")
		if pattern == "" {
			continue
		}
		rules = append(rules, ignoreRule{pattern: pattern, isDir: isDir})
	}
	return rules
}

// isIgnored checks whether a relative path (forward-slash separated) matches
// any of the ignore rules.
//
// Rules work as follows:
//   - "dir/"   matches any path component named "dir" and everything below it.
//   - "*.ext"  matches files whose name matches the glob (filepath.Match).
//   - "name"   matches any path component exactly equal to "name" (file or dir).
//   - "a/b"    matches paths that start with "a/b" (prefix match).
func isIgnored(relPath string, rules []ignoreRule) bool {
	parts := strings.Split(relPath, "/")
	name := parts[len(parts)-1]

	for _, rule := range rules {
		// Prefix / exact path match (e.g. "docs/internal" or "build").
		if strings.Contains(rule.pattern, "/") {
			if relPath == rule.pattern || strings.HasPrefix(relPath, rule.pattern+"/") {
				return true
			}
			continue
		}

		// Directory-only rule: match any path component.
		if rule.isDir {
			for _, p := range parts {
				if p == rule.pattern {
					return true
				}
			}
			continue
		}

		// Glob pattern (e.g. "*.generated.go").
		if strings.ContainsAny(rule.pattern, "*?[") {
			if matched, _ := filepath.Match(rule.pattern, name); matched {
				return true
			}
			continue
		}

		// Bare name: match any path component (file or directory).
		for _, p := range parts {
			if p == rule.pattern {
				return true
			}
		}
	}
	return false
}

// filterIgnored removes paths that match any waveignore rule.
func filterIgnored(files []string, rules []ignoreRule) []string {
	if len(rules) == 0 {
		return files
	}
	filtered := make([]string, 0, len(files))
	for _, f := range files {
		if !isIgnored(f, rules) {
			filtered = append(filtered, f)
		}
	}
	return filtered
}
