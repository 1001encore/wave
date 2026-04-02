package query

import (
	"context"
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
