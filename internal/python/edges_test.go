package python

import (
	"context"
	"testing"

	"github.com/1001encore/wave/internal/indexer"
	"github.com/1001encore/wave/internal/store"
	"github.com/1001encore/wave/internal/syntax"
)

func TestDeriveEdgesAddsReadWriteAndCallEdges(t *testing.T) {
	source := []byte(`import dep

VALUE = dep.load()

class Worker:
    pass

def run():
    worker = Worker()
    current = VALUE
    dep.load()
    return current
`)

	chunks, err := SyntaxExtractor{}.Extract(context.Background(), "example.py", source)
	if err != nil {
		t.Fatalf("extract chunks: %v", err)
	}

	req := indexer.DeriveRequest{
		FileSources: map[string][]byte{
			"example.py": source,
		},
		Chunks: withPrimarySymbols(toChunkData(chunks)),
		Occurrences: []store.OccurrenceData{
			{
				FilePath:           "example.py",
				Symbol:             "pkg dep",
				StartLine:          0,
				StartCol:           7,
				EndLine:            0,
				EndCol:             10,
				EnclosingStartLine: 0,
				EnclosingStartCol:  0,
				EnclosingEndLine:   0,
				EnclosingEndCol:    10,
				IsImport:           true,
			},
			{
				FilePath:           "example.py",
				Symbol:             "local VALUE",
				StartLine:          2,
				StartCol:           0,
				EndLine:            2,
				EndCol:             5,
				EnclosingStartLine: 2,
				EnclosingStartCol:  0,
				EnclosingEndLine:   2,
				EnclosingEndCol:    18,
				IsDefinition:       true,
				IsWrite:            true,
			},
			{
				FilePath:           "example.py",
				Symbol:             "pkg dep/load().",
				StartLine:          2,
				StartCol:           8,
				EndLine:            2,
				EndCol:             16,
				EnclosingStartLine: 2,
				EnclosingStartCol:  0,
				EnclosingEndLine:   2,
				EnclosingEndCol:    18,
				IsRead:             true,
			},
			{
				FilePath:           "example.py",
				Symbol:             "example.py/Worker#",
				StartLine:          4,
				StartCol:           0,
				EndLine:            5,
				EndCol:             8,
				EnclosingStartLine: 4,
				EnclosingStartCol:  0,
				EnclosingEndLine:   5,
				EnclosingEndCol:    8,
				IsDefinition:       true,
			},
			{
				FilePath:           "example.py",
				Symbol:             "example.py/run().",
				StartLine:          7,
				StartCol:           4,
				EndLine:            11,
				EndCol:             18,
				EnclosingStartLine: 7,
				EnclosingStartCol:  0,
				EnclosingEndLine:   11,
				EnclosingEndCol:    18,
				IsDefinition:       true,
			},
			{
				FilePath:           "example.py",
				Symbol:             "local worker",
				StartLine:          8,
				StartCol:           4,
				EndLine:            8,
				EndCol:             10,
				EnclosingStartLine: 8,
				EnclosingStartCol:  4,
				EnclosingEndLine:   8,
				EnclosingEndCol:    21,
				IsDefinition:       true,
				IsWrite:            true,
			},
			{
				FilePath:           "example.py",
				Symbol:             "example.py/Worker#",
				StartLine:          8,
				StartCol:           13,
				EndLine:            8,
				EndCol:             19,
				EnclosingStartLine: 8,
				EnclosingStartCol:  4,
				EnclosingEndLine:   8,
				EnclosingEndCol:    21,
				IsRead:             true,
			},
			{
				FilePath:           "example.py",
				Symbol:             "local current",
				StartLine:          9,
				StartCol:           4,
				EndLine:            9,
				EndCol:             11,
				EnclosingStartLine: 9,
				EnclosingStartCol:  4,
				EnclosingEndLine:   9,
				EnclosingEndCol:    19,
				IsDefinition:       true,
				IsWrite:            true,
			},
			{
				FilePath:           "example.py",
				Symbol:             "local VALUE",
				StartLine:          9,
				StartCol:           14,
				EndLine:            9,
				EndCol:             19,
				EnclosingStartLine: 9,
				EnclosingStartCol:  4,
				EnclosingEndLine:   9,
				EnclosingEndCol:    19,
				IsRead:             true,
			},
			{
				FilePath:           "example.py",
				Symbol:             "pkg dep/load().",
				StartLine:          10,
				StartCol:           4,
				EndLine:            10,
				EndCol:             12,
				EnclosingStartLine: 10,
				EnclosingStartCol:  4,
				EnclosingEndLine:   10,
				EnclosingEndCol:    14,
				IsRead:             true,
			},
			{
				FilePath:           "example.py",
				Symbol:             "local current",
				StartLine:          11,
				StartCol:           11,
				EndLine:            11,
				EndCol:             18,
				EnclosingStartLine: 11,
				EnclosingStartCol:  4,
				EnclosingEndLine:   11,
				EnclosingEndCol:    18,
				IsRead:             true,
			},
		},
		Symbols: map[string]store.SymbolData{
			"example.py/Worker#": {ScipSymbol: "example.py/Worker#", Kind: "class"},
		},
	}

	edges, err := deriveEdges(context.Background(), req)
	if err != nil {
		t.Fatalf("derive edges: %v", err)
	}

	assertEdge(t, edges, "example.py", "pkg dep", "imports")
	assertEdge(t, edges, "example.py/run().", "local VALUE", "reads")
	assertEdge(t, edges, "example.py/run().", "local current", "writes")
	assertEdge(t, edges, "example.py/run().", "pkg dep/load().", "calls")
	assertEdge(t, edges, "example.py/run().", "local current", "returns")
	assertEdge(t, edges, "example.py/run().", "local current", "defines")
	assertEdge(t, edges, "example.py/run().", "example.py/Worker#", "instantiates")
}

func toChunkData(chunks []syntax.Chunk) []store.ChunkData {
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

func withPrimarySymbols(chunks []store.ChunkData) []store.ChunkData {
	for i := range chunks {
		switch {
		case chunks[i].Kind == "module":
			chunks[i].PrimarySymbol = "example.py"
		case chunks[i].Kind == "function_definition" && chunks[i].Name == "run":
			chunks[i].PrimarySymbol = "example.py/run()."
		}
	}
	return chunks
}

func assertEdge(t *testing.T, edges []store.EdgeData, src string, dst string, kind string) {
	t.Helper()
	for _, edge := range edges {
		if edge.SrcSymbol == src && edge.DstSymbol == dst && edge.Kind == kind {
			return
		}
	}
	t.Fatalf("missing edge %q -> %q kind=%q", src, dst, kind)
}
