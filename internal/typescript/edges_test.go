package typescript

import (
	"context"
	"testing"

	"github.com/1001encore/wave/internal/indexer"
	"github.com/1001encore/wave/internal/store"
	"github.com/1001encore/wave/internal/syntax"
)

func TestDeriveEdgesAddsDefinesReturnAndInstantiateEdges(t *testing.T) {
	source := []byte(`import { dep } from "./dep";

class Service {}

function run() {
  const service = new Service();
  const value = dep.load();
  return value;
}
`)

	chunks, err := SyntaxExtractor{}.Extract(context.Background(), "example.ts", source)
	if err != nil {
		t.Fatalf("extract chunks: %v", err)
	}

	req := indexer.DeriveRequest{
		FileSources: map[string][]byte{
			"example.ts": source,
		},
		Chunks: withTSPrimarySymbols(toTSChunkData(chunks)),
		Occurrences: []store.OccurrenceData{
			{
				FilePath:           "example.ts",
				Symbol:             "pkg dep",
				StartLine:          0,
				StartCol:           9,
				EndLine:            0,
				EndCol:             12,
				EnclosingStartLine: 0,
				EnclosingStartCol:  0,
				EnclosingEndLine:   0,
				EnclosingEndCol:    28,
				IsImport:           true,
			},
			{
				FilePath:           "example.ts",
				Symbol:             "example.ts/Service#",
				StartLine:          2,
				StartCol:           6,
				EndLine:            2,
				EndCol:             13,
				EnclosingStartLine: 2,
				EnclosingStartCol:  0,
				EnclosingEndLine:   2,
				EnclosingEndCol:    15,
				IsDefinition:       true,
			},
			{
				FilePath:           "example.ts",
				Symbol:             "example.ts/run().",
				StartLine:          4,
				StartCol:           9,
				EndLine:            8,
				EndCol:             1,
				EnclosingStartLine: 4,
				EnclosingStartCol:  0,
				EnclosingEndLine:   8,
				EnclosingEndCol:    1,
				IsDefinition:       true,
			},
			{
				FilePath:           "example.ts",
				Symbol:             "local service",
				StartLine:          5,
				StartCol:           8,
				EndLine:            5,
				EndCol:             15,
				EnclosingStartLine: 5,
				EnclosingStartCol:  2,
				EnclosingEndLine:   5,
				EnclosingEndCol:    32,
				IsDefinition:       true,
				IsWrite:            true,
			},
			{
				FilePath:           "example.ts",
				Symbol:             "example.ts/Service#",
				StartLine:          5,
				StartCol:           22,
				EndLine:            5,
				EndCol:             29,
				EnclosingStartLine: 5,
				EnclosingStartCol:  2,
				EnclosingEndLine:   5,
				EnclosingEndCol:    32,
				IsRead:             true,
			},
			{
				FilePath:           "example.ts",
				Symbol:             "local value",
				StartLine:          6,
				StartCol:           8,
				EndLine:            6,
				EndCol:             13,
				EnclosingStartLine: 6,
				EnclosingStartCol:  2,
				EnclosingEndLine:   6,
				EnclosingEndCol:    27,
				IsDefinition:       true,
				IsWrite:            true,
			},
			{
				FilePath:           "example.ts",
				Symbol:             "pkg dep/load().",
				StartLine:          6,
				StartCol:           16,
				EndLine:            6,
				EndCol:             24,
				EnclosingStartLine: 6,
				EnclosingStartCol:  2,
				EnclosingEndLine:   6,
				EnclosingEndCol:    27,
				IsRead:             true,
			},
			{
				FilePath:           "example.ts",
				Symbol:             "local value",
				StartLine:          7,
				StartCol:           9,
				EndLine:            7,
				EndCol:             14,
				EnclosingStartLine: 7,
				EnclosingStartCol:  2,
				EnclosingEndLine:   7,
				EnclosingEndCol:    15,
				IsRead:             true,
			},
		},
		Symbols: map[string]store.SymbolData{
			"example.ts/Service#": {ScipSymbol: "example.ts/Service#", Kind: "class"},
		},
	}

	edges, err := deriveEdges(context.Background(), req)
	if err != nil {
		t.Fatalf("derive edges: %v", err)
	}

	assertTSEdge(t, edges, "example.ts", "pkg dep", "imports")
	assertTSEdge(t, edges, "example.ts/run().", "local value", "defines")
	assertTSEdge(t, edges, "example.ts/run().", "local value", "returns")
	assertTSEdge(t, edges, "example.ts/run().", "example.ts/Service#", "instantiates")
}

func toTSChunkData(chunks []syntax.Chunk) []store.ChunkData {
	out := make([]store.ChunkData, 0, len(chunks))
	for _, chunk := range chunks {
		out = append(out, store.ChunkData{
			Key:           chunk.Key,
			FilePath:      chunk.FilePath,
			Kind:          chunk.Kind,
			Name:          chunk.Name,
			ParentKey:     chunk.ParentKey,
			StartByte:     chunk.StartByte,
			EndByte:       chunk.EndByte,
			StartLine:     chunk.StartLine,
			StartCol:      chunk.StartCol,
			EndLine:       chunk.EndLine,
			EndCol:        chunk.EndCol,
			Text:          chunk.Text,
			HeaderText:    chunk.HeaderText,
			PrimarySymbol: chunk.PrimarySymbol,
		})
	}
	return out
}

func withTSPrimarySymbols(chunks []store.ChunkData) []store.ChunkData {
	for i := range chunks {
		switch {
		case chunks[i].Kind == "module":
			chunks[i].PrimarySymbol = "example.ts"
		case chunks[i].Kind == "function_declaration" && chunks[i].Name == "run":
			chunks[i].PrimarySymbol = "example.ts/run()."
		case chunks[i].Kind == "class_declaration" && chunks[i].Name == "Service":
			chunks[i].PrimarySymbol = "example.ts/Service#"
		}
	}
	return chunks
}

func assertTSEdge(t *testing.T, edges []store.EdgeData, src string, dst string, kind string) {
	t.Helper()
	for _, edge := range edges {
		if edge.SrcSymbol == src && edge.DstSymbol == dst && edge.Kind == kind {
			return
		}
	}
	t.Fatalf("missing edge %q -> %q kind=%q", src, dst, kind)
}
