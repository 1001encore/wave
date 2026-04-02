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
