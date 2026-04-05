package python

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/1001encore/wave/internal/indexer"
	"github.com/1001encore/wave/internal/store"
	"github.com/1001encore/wave/internal/syntax"
	"github.com/1001encore/wave/internal/vcs"
	"github.com/1001encore/wave/internal/workspace"
)

type Adapter struct{}

var manifests = []string{"pyproject.toml", "setup.py", "setup.cfg"}

func (Adapter) ID() string       { return "python-scip" }
func (Adapter) Language() string { return "python" }

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
		for _, manifest := range manifests {
			candidate := filepath.Join(current, manifest)
			if fileExists(candidate) {
				return workspace.Unit{
					RootPath:          current,
					Language:          "python",
					ManifestPath:      candidate,
					Name:              sanitizeProjectName(filepath.Base(current)),
					EnvironmentSource: detectEnvironmentSource(current),
					AdapterID:         "python-scip",
				}, nil
			}
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return workspace.Unit{}, fmt.Errorf("no supported Python project manifest found from %s", absStart)
}

func (Adapter) Validate(ctx context.Context, unit workspace.Unit) error {
	_ = ctx
	_ = unit
	required := []string{"python3", "node", "scip-python"}
	for _, name := range required {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("required tool %q not found: %w", name, err)
		}
	}
	return nil
}

func (Adapter) Index(ctx context.Context, unit workspace.Unit, artifactPath string) (indexer.Result, error) {
	cmd := exec.CommandContext(
		ctx,
		"scip-python",
		"index",
		"--cwd", unit.RootPath,
		"--output", artifactPath,
		"--project-name", unit.Name,
		"--quiet",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return indexer.Result{}, fmt.Errorf("run scip-python: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	versionCmd := exec.CommandContext(ctx, "scip-python", "--version")
	versionOutput, _ := versionCmd.CombinedOutput()

	return indexer.Result{
		ArtifactPath: filepath.Clean(artifactPath),
		ToolName:     "scip-python",
		ToolVersion:  strings.TrimSpace(string(versionOutput)),
	}, nil
}

func (Adapter) SourceFiles(unit workspace.Unit) ([]string, error) {
	return vcs.SourceFiles(unit.RootPath, []string{".py"}, []string{".git", ".wave", ".venv", "venv", "__pycache__"})
}

func (Adapter) SyntaxExtractor() syntax.Extractor {
	return SyntaxExtractor{}
}

func (Adapter) DeriveEdges(ctx context.Context, req indexer.DeriveRequest) ([]store.EdgeData, error) {
	return deriveEdges(ctx, req)
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
	trimmed = strings.TrimSuffix(trimmed, ").")
	trimmed = strings.TrimSuffix(trimmed, "()")
	if idx := strings.Index(trimmed, "("); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	trimmed = strings.Trim(trimmed, "`")
	if trimmed != "" {
		return trimmed
	}
	return symbol
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func detectEnvironmentSource(root string) string {
	if venv := os.Getenv("VIRTUAL_ENV"); venv != "" {
		return venv
	}
	if dirExists(filepath.Join(root, ".venv")) {
		return filepath.Join(root, ".venv")
	}
	if dirExists(filepath.Join(root, "venv")) {
		return filepath.Join(root, "venv")
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
