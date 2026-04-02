package indexer

import (
	"context"
	"fmt"

	"github.com/1001encore/wave/internal/store"
	"github.com/1001encore/wave/internal/syntax"
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

func DetectUnit(adapters []Adapter, start string) (Adapter, workspace.Unit, error) {
	var errs []error
	for _, adapter := range adapters {
		unit, err := adapter.Detect(start)
		if err == nil {
			return adapter, unit, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", adapter.ID(), err))
	}
	return nil, workspace.Unit{}, fmt.Errorf("no supported workspace unit found from %s", start)
}
