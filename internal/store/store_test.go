package store

import (
	"context"
	"path/filepath"
	"testing"
)

func singleSymbolPayload(root string, adapterID string, language string, manifest string, tool string, relPath string, symbol string, displayName string) IndexPayload {
	return IndexPayload{
		Project: ProjectData{
			RootPath:          root,
			Name:              "project",
			Language:          language,
			ManifestPath:      manifest,
			EnvironmentSource: "",
			AdapterID:         adapterID,
			ScipArtifactPath:  filepath.Join(root, ".wave", "artifacts", "index."+adapterID+".scip"),
			ToolName:          tool,
			ToolVersion:       "test",
		},
		Files: []FileData{
			{
				RelativePath: relPath,
				AbsPath:      filepath.Join(root, relPath),
				Language:     language,
				ContentHash:  "hash-" + relPath,
			},
		},
		Symbols: []SymbolData{
			{
				ScipSymbol:   symbol,
				DisplayName:  displayName,
				Kind:         "function",
				FilePath:     relPath,
				DefStartLine: 0,
				DefStartCol:  0,
				DefEndLine:   0,
				DefEndCol:    8,
			},
		},
	}
}

func TestListReferencesBySymbolIDIncludesReferenceFamily(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	st, err := Open(filepath.Join(tmp, "wave.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	root := filepath.Join(tmp, "project")
	payload := IndexPayload{
		Project: ProjectData{
			RootPath:          root,
			Name:              "project",
			Language:          "python",
			ManifestPath:      filepath.Join(root, "pyproject.toml"),
			EnvironmentSource: "",
			AdapterID:         "python-scip",
			ScipArtifactPath:  filepath.Join(root, ".wave", "artifacts", "index.scip"),
			ToolName:          "scip-python",
			ToolVersion:       "test",
		},
		Files: []FileData{
			{RelativePath: "animal.py", AbsPath: filepath.Join(root, "animal.py"), Language: "python", ContentHash: "a"},
			{RelativePath: "dog.py", AbsPath: filepath.Join(root, "dog.py"), Language: "python", ContentHash: "b"},
		},
		Symbols: []SymbolData{
			{
				ScipSymbol:   "Animal#sound()",
				DisplayName:  "sound",
				Kind:         "method",
				FilePath:     "animal.py",
				DefStartLine: 0,
				DefEndLine:   1,
			},
			{
				ScipSymbol:   "Dog#sound()",
				DisplayName:  "sound",
				Kind:         "method",
				FilePath:     "dog.py",
				DefStartLine: 0,
				DefEndLine:   1,
			},
		},
		Occurrences: []OccurrenceData{
			{
				FilePath:     "animal.py",
				Symbol:       "Animal#sound()",
				StartLine:    5,
				StartCol:     2,
				EndLine:      5,
				EndCol:       7,
				SyntaxKind:   "identifier_function",
				IsDefinition: false,
			},
			{
				FilePath:     "dog.py",
				Symbol:       "Dog#sound()",
				StartLine:    8,
				StartCol:     2,
				EndLine:      8,
				EndCol:       7,
				SyntaxKind:   "identifier_function",
				IsDefinition: false,
			},
		},
		Edges: []EdgeData{
			{SrcSymbol: "Dog#sound()", DstSymbol: "Animal#sound()", Kind: "reference", Provenance: "scip"},
		},
	}

	if err := st.ReplaceProjectIndex(ctx, payload); err != nil {
		t.Fatalf("replace project index: %v", err)
	}

	defs, err := st.FindDefinitions(ctx, root, "Animal#sound()", 5)
	if err != nil {
		t.Fatalf("find definitions: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("definitions = %d, want 1", len(defs))
	}

	refs, err := st.ListReferencesBySymbolID(ctx, root, defs[0].SymbolID, 10)
	if err != nil {
		t.Fatalf("list references: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("reference count = %d, want 2", len(refs))
	}
}

func TestLinkedSymbolsForChunksReturnsPersistedBridgeLinks(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	st, err := Open(filepath.Join(tmp, "wave.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	root := filepath.Join(tmp, "project")
	payload := IndexPayload{
		Project: ProjectData{
			RootPath:          root,
			Name:              "project",
			Language:          "python",
			ManifestPath:      filepath.Join(root, "pyproject.toml"),
			EnvironmentSource: "",
			AdapterID:         "python-scip",
			ScipArtifactPath:  filepath.Join(root, ".wave", "artifacts", "index.scip"),
			ToolName:          "scip-python",
			ToolVersion:       "test",
		},
		Files: []FileData{
			{RelativePath: "svc.py", AbsPath: filepath.Join(root, "svc.py"), Language: "python", ContentHash: "a"},
		},
		Symbols: []SymbolData{
			{ScipSymbol: "pkg handler()", DisplayName: "handler", Kind: "function", FilePath: "svc.py", DefStartLine: 0, DefEndLine: 1},
			{ScipSymbol: "pkg client", DisplayName: "client", Kind: "variable", FilePath: "svc.py", DefStartLine: 2, DefEndLine: 2},
		},
		Chunks: []ChunkData{
			{
				Key:           "chunk:handler",
				FilePath:      "svc.py",
				Kind:          "function_definition",
				Name:          "handler",
				StartLine:     0,
				EndLine:       5,
				Text:          "def handler(): pass",
				HeaderText:    "def handler():",
				RetrievalText: "handler",
				PrimarySymbol: "pkg handler()",
			},
		},
		ChunkSymbols: []ChunkSymbolLinkData{
			{ChunkKey: "chunk:handler", Symbol: "pkg handler()", Role: "defines", Weight: 1.0},
			{ChunkKey: "chunk:handler", Symbol: "pkg client", Role: "reads", Weight: 0.6},
		},
	}

	if err := st.ReplaceProjectIndex(ctx, payload); err != nil {
		t.Fatalf("replace project index: %v", err)
	}

	def, err := st.FindDefinition(ctx, root, "pkg handler()")
	if err != nil {
		t.Fatalf("find definition: %v", err)
	}
	if def == nil {
		t.Fatal("expected definition")
	}

	chunk, err := st.DefinitionChunk(ctx, root, def.SymbolID)
	if err != nil {
		t.Fatalf("definition chunk: %v", err)
	}
	if chunk == nil {
		t.Fatal("expected chunk")
	}

	links, err := st.LinkedSymbolsForChunks(ctx, root, []int64{chunk.ChunkID})
	if err != nil {
		t.Fatalf("linked symbols: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("link count = %d, want 2", len(links))
	}
}

func TestReplaceProjectIndexReplacesOnlyMatchingAdapter(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	st, err := Open(filepath.Join(tmp, "wave.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	root := filepath.Join(tmp, "workspace")
	pyPayloadV1 := singleSymbolPayload(
		root,
		"python-scip",
		"python",
		filepath.Join(root, "pyproject.toml"),
		"scip-python",
		"svc.py",
		"py pkg old_handler()",
		"old_handler",
	)
	tsPayload := singleSymbolPayload(
		root,
		"typescript-scip",
		"typescript",
		filepath.Join(root, "tsconfig.json"),
		"scip-typescript",
		"src/index.ts",
		"ts pkg ts_handler()",
		"ts_handler",
	)
	pyPayloadV2 := singleSymbolPayload(
		root,
		"python-scip",
		"python",
		filepath.Join(root, "pyproject.toml"),
		"scip-python",
		"svc.py",
		"py pkg new_handler()",
		"new_handler",
	)

	if err := st.ReplaceProjectIndex(ctx, pyPayloadV1); err != nil {
		t.Fatalf("replace python index v1: %v", err)
	}
	if err := st.ReplaceProjectIndex(ctx, tsPayload); err != nil {
		t.Fatalf("replace typescript index: %v", err)
	}
	if err := st.ReplaceProjectIndex(ctx, pyPayloadV2); err != nil {
		t.Fatalf("replace python index v2: %v", err)
	}

	oldPy, err := st.FindDefinition(ctx, root, "old_handler")
	if err != nil {
		t.Fatalf("find old python definition: %v", err)
	}
	if oldPy != nil {
		t.Fatal("expected old python definition to be replaced")
	}

	newPy, err := st.FindDefinition(ctx, root, "new_handler")
	if err != nil {
		t.Fatalf("find new python definition: %v", err)
	}
	if newPy == nil {
		t.Fatal("expected new python definition")
	}

	tsDef, err := st.FindDefinition(ctx, root, "ts_handler")
	if err != nil {
		t.Fatalf("find typescript definition: %v", err)
	}
	if tsDef == nil {
		t.Fatal("expected typescript definition to remain indexed")
	}
}

func TestSearchSymbolsAggregatesAcrossAdaptersForSameRoot(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	st, err := Open(filepath.Join(tmp, "wave.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	root := filepath.Join(tmp, "workspace")
	pyPayload := singleSymbolPayload(
		root,
		"python-scip",
		"python",
		filepath.Join(root, "pyproject.toml"),
		"scip-python",
		"svc.py",
		"py pkg py_handler()",
		"py_handler",
	)
	tsPayload := singleSymbolPayload(
		root,
		"typescript-scip",
		"typescript",
		filepath.Join(root, "tsconfig.json"),
		"scip-typescript",
		"src/index.ts",
		"ts pkg ts_handler()",
		"ts_handler",
	)

	if err := st.ReplaceProjectIndex(ctx, pyPayload); err != nil {
		t.Fatalf("replace python index: %v", err)
	}
	if err := st.ReplaceProjectIndex(ctx, tsPayload); err != nil {
		t.Fatalf("replace typescript index: %v", err)
	}

	hits, err := st.SearchSymbols(ctx, root, "handler", 10)
	if err != nil {
		t.Fatalf("search symbols: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("symbol hit count = %d, want 2", len(hits))
	}
}
