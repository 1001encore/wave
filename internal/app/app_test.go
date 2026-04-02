package app

import (
	"testing"

	"github.com/1001encore/wave/internal/store"
)

func TestDeriveChunkSymbolLinksIncludesPrimaryAndOccurrenceRoles(t *testing.T) {
	chunks := []store.ChunkData{
		{
			Key:           "chunk:handler",
			FilePath:      "svc.py",
			Kind:          "function_definition",
			Name:          "handler",
			StartLine:     10,
			StartCol:      0,
			EndLine:       20,
			EndCol:        0,
			PrimarySymbol: "pkg handler()",
		},
	}
	occurrences := []store.OccurrenceData{
		{
			FilePath:   "svc.py",
			Symbol:     "dep client",
			StartLine:  12,
			StartCol:   4,
			EndLine:    12,
			EndCol:     10,
			SyntaxKind: "identifier",
			IsRead:     true,
		},
		{
			FilePath:   "svc.py",
			Symbol:     "dep config",
			StartLine:  13,
			StartCol:   4,
			EndLine:    13,
			EndCol:     10,
			SyntaxKind: "identifier",
			IsWrite:    true,
		},
	}

	links := deriveChunkSymbolLinks(chunks, occurrences)
	got := map[string]float64{}
	for _, link := range links {
		got[link.Symbol+"|"+link.Role] = link.Weight
	}

	if got["pkg handler()|defines"] != 1.0 {
		t.Fatalf("primary symbol define weight = %v, want 1.0", got["pkg handler()|defines"])
	}
	if got["dep client|reads"] != 0.6 {
		t.Fatalf("read link weight = %v, want 0.6", got["dep client|reads"])
	}
	if got["dep client|uses"] != 0.55 {
		t.Fatalf("use link weight = %v, want 0.55", got["dep client|uses"])
	}
	if got["dep config|writes"] != 0.75 {
		t.Fatalf("write link weight = %v, want 0.75", got["dep config|writes"])
	}
}
