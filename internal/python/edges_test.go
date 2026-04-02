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

def run():
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
				Symbol:             "example.py/run().",
				StartLine:          4,
				StartCol:           4,
				EndLine:            7,
				EndCol:             18,
				EnclosingStartLine: 4,
				EnclosingStartCol:  0,
				EnclosingEndLine:   7,
				EnclosingEndCol:    18,
				IsDefinition:       true,
			},
			{
				FilePath:           "example.py",
				Symbol:             "local current",
				StartLine:          5,
				StartCol:           4,
				EndLine:            5,
				EndCol:             11,
				EnclosingStartLine: 5,
				EnclosingStartCol:  4,
				EnclosingEndLine:   5,
				EnclosingEndCol:    19,
				IsWrite:            true,
			},
			{
				FilePath:           "example.py",
				Symbol:             "local VALUE",
				StartLine:          5,
				StartCol:           14,
				EndLine:            5,
				EndCol:             19,
				EnclosingStartLine: 5,
				EnclosingStartCol:  4,
				EnclosingEndLine:   5,
				EnclosingEndCol:    19,
				IsRead:             true,
			},
			{
				FilePath:           "example.py",
				Symbol:             "pkg dep/load().",
				StartLine:          6,
				StartCol:           4,
				EndLine:            6,
				EndCol:             12,
				EnclosingStartLine: 6,
				EnclosingStartCol:  4,
				EnclosingEndLine:   6,
				EnclosingEndCol:    14,
				IsRead:             true,
			},
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
