package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Paths struct {
	RootDir     string
	StateDir    string
	ArtifactDir string
	DBPath      string
}

func Resolve(root string, dbOverride string) (Paths, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve root: %w", err)
	}

	stateDir := filepath.Join(absRoot, ".wave")
	artifactDir := filepath.Join(stateDir, "artifacts")
	dbPath := dbOverride
	if dbPath == "" {
		dbPath = filepath.Join(stateDir, "wave.db")
	}
	if !filepath.IsAbs(dbPath) {
		dbPath, err = filepath.Abs(dbPath)
		if err != nil {
			return Paths{}, fmt.Errorf("resolve db path: %w", err)
		}
	}

	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return Paths{}, fmt.Errorf("create state directories: %w", err)
	}

	return Paths{
		RootDir:     absRoot,
		StateDir:    stateDir,
		ArtifactDir: artifactDir,
		DBPath:      dbPath,
	}, nil
}
