package query

import (
	"testing"

	"github.com/1001encore/wave/internal/store"
)

func TestResolveDefinitionUniqueExact(t *testing.T) {
	candidates := []store.DefinitionResult{
		{DisplayName: "setup_logging", Path: "/tmp/logging.py"},
	}

	def, ambiguous := resolveDefinition("setup_logging", candidates)
	if ambiguous {
		t.Fatal("expected unique definition, got ambiguity")
	}
	if def == nil || def.DisplayName != "setup_logging" {
		t.Fatalf("unexpected definition: %#v", def)
	}
}

func TestResolveDefinitionAmbiguousExactName(t *testing.T) {
	candidates := []store.DefinitionResult{
		{DisplayName: "timeline", Path: "/tmp/a.py"},
		{DisplayName: "timeline", Path: "/tmp/b.py"},
	}

	def, ambiguous := resolveDefinition("timeline", candidates)
	if def == nil {
		t.Fatal("expected best-ranked definition, got nil")
	}
	if !ambiguous {
		t.Fatal("expected ambiguity flag to be set")
	}
}

func TestChooseContextSeedPrefersFocusedChunkOverModule(t *testing.T) {
	hits := []store.SearchHit{
		{
			ChunkID:    1,
			Path:       "/tmp/service.py",
			Kind:       "module",
			Name:       "service.py",
			StartLine:  0,
			EndLine:    3500,
			Score:      8.0,
			HeaderText: "module",
		},
		{
			ChunkID:    2,
			Path:       "/tmp/service.py",
			Kind:       "function_definition",
			Name:       "object_timeline",
			StartLine:  40,
			EndLine:    68,
			Score:      4.5,
			HeaderText: "def object_timeline(",
		},
	}

	seed := chooseContextSeed(hits, "object timeline")
	if seed.ChunkID != 2 {
		t.Fatalf("seed chunk = %d, want 2", seed.ChunkID)
	}
}
