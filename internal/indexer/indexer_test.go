package indexer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/1001encore/wave/internal/store"
	"github.com/1001encore/wave/internal/syntax"
	"github.com/1001encore/wave/internal/workspace"
)

type fakeAdapter struct {
	id        string
	language  string
	unit      workspace.Unit
	detectErr error
}

func (f fakeAdapter) ID() string { return f.id }
func (f fakeAdapter) Language() string {
	return f.language
}
func (f fakeAdapter) Detect(start string) (workspace.Unit, error) {
	_ = start
	if f.detectErr != nil {
		return workspace.Unit{}, f.detectErr
	}
	return f.unit, nil
}
func (f fakeAdapter) Validate(context.Context, workspace.Unit) error { return nil }
func (f fakeAdapter) Index(context.Context, workspace.Unit, string) (Result, error) {
	return Result{}, nil
}
func (f fakeAdapter) SourceFiles(workspace.Unit) ([]string, error) { return nil, nil }
func (f fakeAdapter) SyntaxExtractor() syntax.Extractor            { return nil }
func (f fakeAdapter) DeriveEdges(context.Context, DeriveRequest) ([]store.EdgeData, error) {
	return nil, nil
}
func (f fakeAdapter) NormalizeDisplayName(value string) string { return value }
func (f fakeAdapter) Manifests() []string                      { return nil }

func TestDetectUnitsReturnsAllMatchesInOrder(t *testing.T) {
	adapters := []Adapter{
		fakeAdapter{
			id:       "python-scip",
			language: "python",
			unit: workspace.Unit{
				RootPath:  "/repo",
				Language:  "python",
				AdapterID: "python-scip",
			},
		},
		fakeAdapter{
			id:       "typescript-scip",
			language: "typescript",
			unit: workspace.Unit{
				RootPath:  "/repo",
				Language:  "typescript",
				AdapterID: "typescript-scip",
			},
		},
	}

	units, err := DetectUnits(adapters, "/repo")
	if err != nil {
		t.Fatalf("detect units: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("unit count = %d, want 2", len(units))
	}
	if units[0].Adapter.ID() != "python-scip" || units[1].Adapter.ID() != "typescript-scip" {
		t.Fatalf("adapter order = [%s, %s], want [python-scip, typescript-scip]", units[0].Adapter.ID(), units[1].Adapter.ID())
	}
}

func TestDetectUnitReturnsFirstDetected(t *testing.T) {
	adapters := []Adapter{
		fakeAdapter{
			id: "python-scip",
			unit: workspace.Unit{
				RootPath:  "/repo",
				Language:  "python",
				AdapterID: "python-scip",
			},
		},
		fakeAdapter{
			id: "typescript-scip",
			unit: workspace.Unit{
				RootPath:  "/repo",
				Language:  "typescript",
				AdapterID: "typescript-scip",
			},
		},
	}

	adapter, unit, err := DetectUnit(adapters, "/repo")
	if err != nil {
		t.Fatalf("detect unit: %v", err)
	}
	if adapter.ID() != "python-scip" {
		t.Fatalf("adapter id = %q, want %q", adapter.ID(), "python-scip")
	}
	if unit.Language != "python" {
		t.Fatalf("unit language = %q, want %q", unit.Language, "python")
	}
}

func TestDetectUnitsFailsWhenNoAdapterMatches(t *testing.T) {
	adapters := []Adapter{
		fakeAdapter{id: "python-scip", detectErr: errors.New("no python project")},
		fakeAdapter{id: "typescript-scip", detectErr: errors.New("no typescript project")},
	}

	if _, err := DetectUnits(adapters, "/repo"); err == nil {
		t.Fatal("expected detect error")
	}
}

// manifestAdapter is a fakeAdapter that also reports manifest filenames,
// enabling DetectAllUnits to discover it during tree walks.
type manifestAdapter struct {
	fakeAdapter
	manifests []string
}

func (m manifestAdapter) Manifests() []string { return m.manifests }

func TestDetectAllUnitsFindsNestedWorkspaces(t *testing.T) {
	root := t.TempDir()

	// Create monorepo structure:
	//   root/services/api/go.mod
	//   root/services/web/package.json
	//   root/services/web/tsconfig.json
	apiDir := filepath.Join(root, "services", "api")
	webDir := filepath.Join(root, "services", "web")
	if err := os.MkdirAll(apiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(webDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(apiDir, "go.mod"), []byte("module api"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "tsconfig.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	adapters := []Adapter{
		manifestAdapter{
			fakeAdapter: fakeAdapter{id: "go-scip", language: "go"},
			manifests:   []string{"go.mod"},
		},
		manifestAdapter{
			fakeAdapter: fakeAdapter{id: "typescript-scip", language: "typescript"},
			manifests:   []string{"tsconfig.json"},
		},
	}

	units, err := DetectAllUnits(adapters, root)
	if err != nil {
		t.Fatalf("DetectAllUnits() error = %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("unit count = %d, want 2", len(units))
	}

	ids := map[string]bool{}
	for _, u := range units {
		ids[u.Adapter.ID()] = true
	}
	if !ids["go-scip"] {
		t.Error("expected go-scip unit")
	}
	if !ids["typescript-scip"] {
		t.Error("expected typescript-scip unit")
	}
}

func TestDetectAllUnitsSkipsNodeModules(t *testing.T) {
	root := t.TempDir()

	// Create a tsconfig.json inside node_modules — should be skipped
	nmDir := filepath.Join(root, "node_modules", "somepkg")
	if err := os.MkdirAll(nmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nmDir, "tsconfig.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	adapters := []Adapter{
		manifestAdapter{
			fakeAdapter: fakeAdapter{id: "typescript-scip", language: "typescript"},
			manifests:   []string{"tsconfig.json"},
		},
	}

	_, err := DetectAllUnits(adapters, root)
	if err == nil {
		t.Fatal("expected error for empty results, got nil")
	}
}
