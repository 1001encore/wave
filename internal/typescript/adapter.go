package typescript

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

const (
	adapterID = "typescript-scip"
	language  = "typescript"
)

func (Adapter) ID() string       { return adapterID }
func (Adapter) Language() string { return language }
func (Adapter) Manifests() []string { return []string{"tsconfig.json"} }

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
		manifestPath := filepath.Join(current, "tsconfig.json")
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

	return workspace.Unit{}, fmt.Errorf("no supported TypeScript project manifest found from %s", absStart)
}

func (Adapter) Validate(ctx context.Context, unit workspace.Unit) error {
	_ = ctx
	_ = unit
	required := []string{"node", "scip-typescript"}
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
		"scip-typescript",
		"index",
		"--output",
		artifactPath,
	)
	cmd.Dir = unit.RootPath

	output, err := cmd.CombinedOutput()
	if err != nil {
		return indexer.Result{}, fmt.Errorf("run scip-typescript: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	versionCmd := exec.CommandContext(ctx, "scip-typescript", "--version")
	versionOutput, _ := versionCmd.CombinedOutput()

	return indexer.Result{
		ArtifactPath: filepath.Clean(artifactPath),
		ToolName:     "scip-typescript",
		ToolVersion:  strings.TrimSpace(string(versionOutput)),
	}, nil
}

func (Adapter) SourceFiles(unit workspace.Unit) ([]string, error) {
	return vcs.SourceFiles(unit.RootPath, []string{".ts", ".tsx", ".mts", ".cts"}, []string{".git", ".wave", "node_modules", "dist", "build", ".next", ".turbo"})
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

func isTypeScriptFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".tsx", ".mts", ".cts":
		return true
	default:
		return false
	}
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
	nodeModules := filepath.Join(root, "node_modules")
	if dirExists(nodeModules) {
		return nodeModules
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
