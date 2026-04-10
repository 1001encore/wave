package indexer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/1001encore/wave/internal/store"
	"github.com/1001encore/wave/internal/syntax"
	"github.com/1001encore/wave/internal/vcs"
	"github.com/1001encore/wave/internal/workspace"
)

type Result struct {
	ArtifactPath string `json:"artifact_path"`
	ToolName     string `json:"tool_name"`
	ToolVersion  string `json:"tool_version"`
}

type Adapter interface {
	ID() string
	Language() string
	Manifests() []string
	Detect(start string) (workspace.Unit, error)
	Validate(context.Context, workspace.Unit) error
	Index(context.Context, workspace.Unit, string) (Result, error)
	SourceFiles(workspace.Unit) ([]string, error)
	SyntaxExtractor() syntax.Extractor
	DeriveEdges(context.Context, DeriveRequest) ([]store.EdgeData, error)
	NormalizeDisplayName(string) string
}

type DeriveRequest struct {
	Unit        workspace.Unit
	FileSources map[string][]byte
	Symbols     map[string]store.SymbolData
	Occurrences []store.OccurrenceData
	Chunks      []store.ChunkData
}

type DetectedUnit struct {
	Adapter Adapter
	Unit    workspace.Unit
}

func DetectUnits(adapters []Adapter, start string) ([]DetectedUnit, error) {
	var (
		units  []DetectedUnit
		seenID = map[string]struct{}{}
	)
	for _, adapter := range adapters {
		unit, err := adapter.Detect(start)
		if err == nil {
			if _, exists := seenID[adapter.ID()]; exists {
				continue
			}
			seenID[adapter.ID()] = struct{}{}
			units = append(units, DetectedUnit{
				Adapter: adapter,
				Unit:    unit,
			})
			continue
		}
	}
	if len(units) == 0 {
		return nil, fmt.Errorf("no supported workspace unit found from %s", start)
	}
	return units, nil
}

func DetectUnit(adapters []Adapter, start string) (Adapter, workspace.Unit, error) {
	units, err := DetectUnits(adapters, start)
	if err != nil {
		return nil, workspace.Unit{}, err
	}
	return units[0].Adapter, units[0].Unit, nil
}

// detectAllSkipDirs are directory names skipped during recursive workspace
// detection to avoid descending into dependency, build, or VCS directories.
var detectAllSkipDirs = map[string]struct{}{
	".git":         {},
	".hg":          {},
	".svn":         {},
	".wave":        {},
	"node_modules": {},
	"vendor":       {},
	".venv":        {},
	"venv":         {},
	"__pycache__":  {},
	"target":       {},
	"build":        {},
	"dist":         {},
	"out":          {},
	".idea":        {},
	".vscode":      {},
	"testdata":     {},
}

// DetectAllUnits walks the directory tree rooted at root and discovers all
// workspace units (projects) by looking for manifest files recognized by the
// given adapters. Unlike DetectUnits which walks upward from a starting point,
// this walks downward to find nested workspaces inside a monorepo.
func DetectAllUnits(adapters []Adapter, root string) ([]DetectedUnit, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root path: %w", err)
	}

	// Build a map from manifest filename to the adapters that recognize it.
	type adapterManifest struct {
		adapter  Adapter
		manifest string
	}
	manifestLookup := map[string][]adapterManifest{}
	for _, adapter := range adapters {
		for _, m := range adapter.Manifests() {
			manifestLookup[m] = append(manifestLookup[m], adapterManifest{
				adapter:  adapter,
				manifest: m,
			})
		}
	}

	type unitKey struct {
		rootPath  string
		adapterID string
	}
	seen := map[unitKey]struct{}{}
	var units []DetectedUnit

	walkErr := filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if _, skip := detectAllSkipDirs[name]; skip && path != absRoot {
				return filepath.SkipDir
			}
			if path != absRoot {
				rel, _ := filepath.Rel(absRoot, path)
				if rel != "" && vcs.IsPathIgnored(absRoot, filepath.ToSlash(rel)) {
					return filepath.SkipDir
				}
			}
			return nil
		}

		ams, ok := manifestLookup[d.Name()]
		if !ok {
			return nil
		}

		dir := filepath.Dir(path)
		for _, am := range ams {
			key := unitKey{rootPath: dir, adapterID: am.adapter.ID()}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}

			rel, _ := filepath.Rel(absRoot, dir)
			name := filepath.Base(dir)
			if rel != "." && rel != "" {
				name = strings.ReplaceAll(filepath.ToSlash(rel), "/", "-")
			}

			units = append(units, DetectedUnit{
				Adapter: am.adapter,
				Unit: workspace.Unit{
					RootPath:     dir,
					Language:     am.adapter.Language(),
					ManifestPath: path,
					Name:         sanitizeDetectName(name),
					AdapterID:    am.adapter.ID(),
				},
			})
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walk directory tree: %w", walkErr)
	}

	if len(units) == 0 {
		return nil, fmt.Errorf("no supported workspace units found under %s", absRoot)
	}
	return units, nil
}

func sanitizeDetectName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "." {
		return "wave-project"
	}
	return strings.ReplaceAll(name, " ", "-")
}
