package indexer

import (
	"context"
	"errors"
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
