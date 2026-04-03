package golang

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/1001encore/wave/internal/indexer"
	"github.com/1001encore/wave/internal/store"
	"github.com/1001encore/wave/internal/syntax"
	"github.com/1001encore/wave/internal/workspace"
)

type Adapter struct{}

const (
	adapterID = "go-scip"
	language  = "go"
)

func (Adapter) ID() string       { return adapterID }
func (Adapter) Language() string { return language }

func (Adapter) Detect(start string) (workspace.Unit, error) {
	absStart, err := filepath.Abs(start)
	if err != nil {
		return workspace.Unit{}, fmt.Errorf("resolve start path: %w", err)
	}

	info, err := os.Stat(absStart)
	if err != nil {
		return workspace.Unit{}, fmt.Errorf("stat start path: %w", err)
	}
	if !info.IsDir() {
		absStart = filepath.Dir(absStart)
	}

	current := absStart
	for {
		manifestPath := filepath.Join(current, "go.mod")
		if fileExists(manifestPath) {
			return workspace.Unit{
				RootPath:          current,
				Language:          language,
				ManifestPath:      manifestPath,
				Name:              sanitizeProjectName(filepath.Base(current)),
				EnvironmentSource: detectEnvironmentSource(current),
				AdapterID:         adapterID,
			}, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return workspace.Unit{}, fmt.Errorf("no supported Go project manifest found from %s", absStart)
}

func (Adapter) Validate(ctx context.Context, unit workspace.Unit) error {
	_ = ctx
	_ = unit

	required := []string{"go", "scip-go"}
	for _, tool := range required {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("required tool %q not found: %w", tool, err)
		}
	}
	return nil
}

func (Adapter) Index(ctx context.Context, unit workspace.Unit, artifactPath string) (indexer.Result, error) {
	cmd := exec.CommandContext(ctx, "scip-go")
	cmd.Dir = unit.RootPath

	output, err := cmd.CombinedOutput()
	if err != nil {
		return indexer.Result{}, fmt.Errorf("run scip-go: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	defaultArtifact := filepath.Join(unit.RootPath, "index.scip")
	if !fileExists(defaultArtifact) {
		return indexer.Result{}, fmt.Errorf("scip-go did not produce %s", defaultArtifact)
	}

	if err := os.Rename(defaultArtifact, artifactPath); err != nil {
		return indexer.Result{}, fmt.Errorf("move scip-go artifact to %s: %w", artifactPath, err)
	}

	versionCmd := exec.CommandContext(ctx, "scip-go", "--version")
	versionOutput, _ := versionCmd.CombinedOutput()

	return indexer.Result{
		ArtifactPath: filepath.Clean(artifactPath),
		ToolName:     "scip-go",
		ToolVersion:  strings.TrimSpace(string(versionOutput)),
	}, nil
}

func (Adapter) SourceFiles(unit workspace.Unit) ([]string, error) {
	var files []string
	err := filepath.WalkDir(unit.RootPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".wave", "vendor", "bin", "testdata":
				if path != unit.RootPath {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".go" {
			return nil
		}
		rel, relErr := filepath.Rel(unit.RootPath, path)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func (Adapter) SyntaxExtractor() syntax.Extractor {
	return SyntaxExtractor{}
}

func (Adapter) DeriveEdges(ctx context.Context, req indexer.DeriveRequest) ([]store.EdgeData, error) {
	_ = ctx
	return indexer.DeriveOccurrenceEdges(req, "import_declaration"), nil
}

func (Adapter) NormalizeDisplayName(symbol string) string {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return ""
	}
	if strings.HasPrefix(symbol, "local ") {
		return strings.TrimSpace(strings.TrimPrefix(symbol, "local "))
	}

	trimmed := strings.TrimRight(symbol, ".#:/!")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		trimmed = trimmed[idx+1:]
	}
	if idx := strings.LastIndex(trimmed, ":"); idx >= 0 {
		trimmed = trimmed[idx+1:]
	}
	trimmed = strings.TrimSuffix(trimmed, ").")
	trimmed = strings.TrimSuffix(trimmed, "()")
	if idx := strings.Index(trimmed, "("); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	trimmed = strings.Trim(trimmed, "`")
	if idx := strings.LastIndexAny(trimmed, "#."); idx >= 0 && idx+1 < len(trimmed) {
		return trimmed[idx+1:]
	}
	if trimmed != "" {
		return trimmed
	}
	return symbol
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func detectEnvironmentSource(root string) string {
	vendor := filepath.Join(root, "vendor")
	if fileInfo, err := os.Stat(vendor); err == nil && fileInfo.IsDir() {
		return vendor
	}
	return ""
}

func sanitizeProjectName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "wave-project"
	}
	return strings.ReplaceAll(name, " ", "-")
}
