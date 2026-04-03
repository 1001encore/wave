package query

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/1001encore/wave/internal/embed"
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

func TestReferencesIncludesAllExactMatchesAcrossLanguages(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	st, err := store.Open(filepath.Join(tmp, "wave.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	root := filepath.Join(tmp, "project")
	payload := store.IndexPayload{
		Project: store.ProjectData{
			RootPath:          root,
			Name:              "project",
			Language:          "polyglot",
			ManifestPath:      filepath.Join(root, "pyproject.toml"),
			EnvironmentSource: "",
			AdapterID:         "mixed",
			ScipArtifactPath:  filepath.Join(root, ".wave", "artifacts", "index.scip"),
			ToolName:          "test-indexer",
			ToolVersion:       "test",
		},
		Files: []store.FileData{
			{RelativePath: "app/main.py", AbsPath: filepath.Join(root, "app/main.py"), Language: "python", ContentHash: "py"},
			{RelativePath: "src/index.ts", AbsPath: filepath.Join(root, "src/index.ts"), Language: "typescript", ContentHash: "ts"},
		},
		Symbols: []store.SymbolData{
			{ScipSymbol: "py greet()", DisplayName: "greet", Kind: "function", FilePath: "app/main.py", DefStartLine: 0, DefEndLine: 1},
			{ScipSymbol: "ts greet()", DisplayName: "greet", Kind: "function", FilePath: "src/index.ts", DefStartLine: 0, DefEndLine: 1},
		},
		Occurrences: []store.OccurrenceData{
			{
				FilePath:     "app/main.py",
				Symbol:       "py greet()",
				StartLine:    3,
				StartCol:     1,
				EndLine:      3,
				EndCol:       6,
				SyntaxKind:   "identifier",
				IsDefinition: false,
			},
			{
				FilePath:     "src/index.ts",
				Symbol:       "ts greet()",
				StartLine:    5,
				StartCol:     2,
				EndLine:      5,
				EndCol:       7,
				SyntaxKind:   "identifier",
				IsDefinition: false,
			},
		},
	}

	if err := st.ReplaceProjectIndex(ctx, payload); err != nil {
		t.Fatalf("replace project index: %v", err)
	}

	router := NewRouter(st, embed.NoopProvider{})
	result, err := router.References(ctx, root, "greet", 10)
	if err != nil {
		t.Fatalf("references: %v", err)
	}
	if result.Definition == nil {
		t.Fatal("expected resolved definition")
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("candidate count = %d, want 2", len(result.Candidates))
	}
	if len(result.References) != 2 {
		t.Fatalf("reference count = %d, want 2", len(result.References))
	}
}

func TestDefinitionExposesAllExactMatchesAcrossLanguages(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	st, err := store.Open(filepath.Join(tmp, "wave.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	root := filepath.Join(tmp, "project")
	payload := store.IndexPayload{
		Project: store.ProjectData{
			RootPath:          root,
			Name:              "project",
			Language:          "polyglot",
			ManifestPath:      filepath.Join(root, "pyproject.toml"),
			EnvironmentSource: "",
			AdapterID:         "mixed",
			ScipArtifactPath:  filepath.Join(root, ".wave", "artifacts", "index.scip"),
			ToolName:          "test-indexer",
			ToolVersion:       "test",
		},
		Files: []store.FileData{
			{RelativePath: "app/main.py", AbsPath: filepath.Join(root, "app/main.py"), Language: "python", ContentHash: "py"},
			{RelativePath: "src/index.ts", AbsPath: filepath.Join(root, "src/index.ts"), Language: "typescript", ContentHash: "ts"},
		},
		Symbols: []store.SymbolData{
			{ScipSymbol: "py greet()", DisplayName: "greet", Kind: "function", FilePath: "app/main.py", DefStartLine: 0, DefEndLine: 1},
			{ScipSymbol: "ts greet()", DisplayName: "greet", Kind: "function", FilePath: "src/index.ts", DefStartLine: 0, DefEndLine: 1},
		},
	}

	if err := st.ReplaceProjectIndex(ctx, payload); err != nil {
		t.Fatalf("replace project index: %v", err)
	}

	router := NewRouter(st, embed.NoopProvider{})
	result, err := router.Definition(ctx, root, "greet")
	if err != nil {
		t.Fatalf("definition: %v", err)
	}
	if result.Definition == nil {
		t.Fatal("expected resolved definition")
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("candidate count = %d, want 2", len(result.Candidates))
	}
}

func TestFilterSemanticHitsAppliesRelativeFloor(t *testing.T) {
	hits := []store.SearchHit{
		{Score: distanceForSimilarity(0.50)},
		{Score: distanceForSimilarity(0.42)},
		{Score: distanceForSimilarity(0.34)},
		{Score: distanceForSimilarity(0.10)},
	}

	filtered := filterSemanticHits(hits, 0.20, 0.70)
	if len(filtered) != 2 {
		t.Fatalf("filtered hit count = %d, want 2", len(filtered))
	}
}

func TestShouldSuppressLowConfidenceSingleTokenSemanticOnly(t *testing.T) {
	signals := querySignals{
		semanticChunks: []store.SearchHit{
			{Score: distanceForSimilarity(0.45)},
		},
	}
	if !shouldSuppressLowConfidence("q9x2v7m4k8p1r6t3n0c5b2h7j4w8", signals, searchOptions{confidenceGate: true}) {
		t.Fatal("expected low-confidence single token semantic-only query to be suppressed")
	}
}

func TestShouldNotSuppressWithLexicalEvidence(t *testing.T) {
	signals := querySignals{
		semanticChunks: []store.SearchHit{
			{Score: distanceForSimilarity(0.40)},
		},
		lexicalChunks: []store.SearchHit{
			{ChunkID: 1},
		},
	}
	if shouldSuppressLowConfidence("search rank fusion", signals, searchOptions{confidenceGate: true}) {
		t.Fatal("expected lexical evidence to bypass confidence suppression")
	}
}

func TestSoftmaxHitScoresNormalizes(t *testing.T) {
	hits := []store.SearchHit{
		{Score: 10},
		{Score: 9},
		{Score: 2},
	}

	probs := softmaxHitScores(hits, 4.0)
	if len(probs) != 3 {
		t.Fatalf("prob count = %d, want 3", len(probs))
	}
	total := probs[0] + probs[1] + probs[2]
	if math.Abs(total-1.0) > 1e-9 {
		t.Fatalf("prob total = %.10f, want 1.0", total)
	}
	if probs[0] <= probs[1] || probs[1] <= probs[2] {
		t.Fatalf("expected descending probabilities, got %#v", probs)
	}
}

func distanceForSimilarity(similarity float64) float64 {
	if similarity <= 0 {
		return 1e9
	}
	return (1.0 / similarity) - 1.0
}
