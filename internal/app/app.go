package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/1001encore/wave/internal/config"
	"github.com/1001encore/wave/internal/embed"
	scip "github.com/1001encore/wave/internal/gen/scippb"
	"github.com/1001encore/wave/internal/indexer"
	"github.com/1001encore/wave/internal/python"
	queryrouter "github.com/1001encore/wave/internal/query"
	"github.com/1001encore/wave/internal/scipgraph"
	"github.com/1001encore/wave/internal/store"
	"github.com/1001encore/wave/internal/typescript"
	"github.com/1001encore/wave/internal/workspace"
)

type commandContext struct {
	rootPath string
	dbPath   string
	jsonOut  bool
	limit    int
	explain  bool
	mode     string
	device   string
}

type defOccurrence struct {
	FilePath string
	Symbol   string
	Range    scipgraph.Range
}

const (
	autoReindexMinChangedFiles  = 24
	autoReindexMinChangedRatio  = 0.18
	autoReindexMissingFileFloor = 8
)

func adapters() []indexer.Adapter {
	return []indexer.Adapter{
		python.Adapter{},
		typescript.Adapter{},
	}
}

func Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		printUsage()
		return 1
	}

	cmd := args[0]
	switch cmd {
	case "index":
		return runIndex(ctx, args[1:])
	case "status":
		return runStatus(ctx, args[1:])
	case "search":
		return runSearch(ctx, args[1:])
	case "def":
		return runDef(ctx, args[1:])
	case "refs":
		return runRefs(ctx, args[1:])
	case "context":
		return runContext(ctx, args[1:])
	case "help", "-h", "--help":
		printUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		printUsage()
		return 1
	}
}

func runIndex(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	cc := bindCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 1
	}

	result, message, err := performIndex(ctx, cc)
	if err != nil {
		return fail(err)
	}
	printResult(cc.jsonOut, result, message)
	return 0
}

func runStatus(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	cc := bindCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 1
	}

	root := cc.rootPath
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fail(err)
		}
		root = cwd
	}
	_, unit, detectErr := indexer.DetectUnit(adapters(), root)
	if detectErr == nil && unit.RootPath != "" {
		root = unit.RootPath
	}

	paths, err := config.Resolve(root, cc.dbPath)
	if err != nil {
		return fail(err)
	}
	st, err := store.Open(paths.DBPath)
	if err != nil {
		return fail(err)
	}
	defer st.Close()

	rows, err := st.Status(ctx, "")
	if detectErr == nil && unit.RootPath != "" {
		rows, err = st.Status(ctx, unit.RootPath)
	}
	if err != nil {
		return fail(err)
	}
	if len(rows) == 0 {
		return fail(errors.New("no indexed projects found"))
	}

	statusRows := make([]map[string]any, 0, len(rows))
	type statusView struct {
		root      string
		name      string
		language  string
		adapter   string
		indexed   string
		files     int
		symbols   int
		chunks    int
		edges     int
		freshness indexer.Freshness
	}
	views := make([]statusView, 0, len(rows))
	for _, row := range rows {
		adapter := adapterByID(row.AdapterID)
		freshness := indexer.Freshness{Status: "unknown"}
		if adapter != nil {
			fresh, freshErr := indexer.ComputeFreshness(ctx, st, adapter, workspace.Unit{
				RootPath:     row.RootPath,
				Language:     row.Language,
				ManifestPath: row.ManifestPath,
				AdapterID:    row.AdapterID,
			})
			if freshErr == nil {
				freshness = fresh
			}
		}
		statusRows = append(statusRows, map[string]any{
			"name":          row.Name,
			"root_path":     row.RootPath,
			"language":      row.Language,
			"manifest_path": row.ManifestPath,
			"adapter_id":    row.AdapterID,
			"indexed_at":    row.IndexedAt,
			"file_count":    row.FileCount,
			"symbol_count":  row.SymbolCount,
			"chunk_count":   row.ChunkCount,
			"edge_count":    row.EdgeCount,
			"freshness":     freshness,
		})
		views = append(views, statusView{
			root:      row.RootPath,
			name:      row.Name,
			language:  row.Language,
			adapter:   row.AdapterID,
			indexed:   row.IndexedAt.Format("2006-01-02 15:04:05Z07:00"),
			files:     row.FileCount,
			symbols:   row.SymbolCount,
			chunks:    row.ChunkCount,
			edges:     row.EdgeCount,
			freshness: freshness,
		})
	}

	if cc.jsonOut {
		printJSON(statusRows)
		return 0
	}
	fmt.Println("Project Status")
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ROOT\tLANG\tADAPTER\tINDEXED\tFILES\tSYMBOLS\tCHUNKS\tEDGES\tFRESHNESS")
	for _, view := range views {
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s (%d changed: dirty=%d new=%d missing=%d, %.1f%%)\n",
			view.root,
			view.language,
			view.adapter,
			view.indexed,
			view.files,
			view.symbols,
			view.chunks,
			view.edges,
			view.freshness.Status,
			view.freshness.ChangedFiles,
			view.freshness.DirtyFiles,
			view.freshness.NewFiles,
			view.freshness.MissingFiles,
			view.freshness.ChangedRatio*100,
		)
	}
	_ = tw.Flush()
	return 0
}

func runSearch(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	cc := bindCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		return fail(errors.New("search query is required"))
	}
	if err := maybeAutoReindex(ctx, cc); err != nil {
		return fail(err)
	}

	adapter, unit, paths, st, err := openUnitStore(cc)
	if err != nil {
		return fail(err)
	}
	_ = paths
	defer st.Close()

	embedder, err := embed.ResolveONNXProvider(unit.RootPath, cc.device)
	if err != nil {
		return fail(err)
	}
	router := queryrouter.NewRouter(st, embedder)
	result, err := router.Search(ctx, unit.RootPath, query, cc.limit, queryrouter.QueryMode(cc.mode))
	if err != nil {
		return fail(err)
	}
	printFreshnessWarning(ctx, st, adapter, unit, cc)
	if cc.jsonOut {
		if cc.explain {
			printJSON(result)
		} else {
			printJSON(result.Hits)
		}
		return 0
	}

	if len(result.Hits) == 0 {
		fmt.Println("No matches.")
		return 0
	}
	if cc.explain {
		fmt.Printf("Route: %s (%s)\n", result.Plan.Mode, result.Plan.Reason)
	}
	fmt.Printf("Matches: %d\n", len(result.Hits))
	for i, hit := range result.Hits {
		fmt.Printf(
			"%d. %s:%d-%d  [%s]  %s\n",
			i+1,
			hit.Path,
			hit.StartLine+1,
			hit.EndLine+1,
			hit.Kind,
			firstNonEmpty(hit.Name, hit.HeaderText),
		)
		fmt.Printf("   score: %.3f\n", hit.Score)
		snippet := truncate(hit.HeaderText, 200)
		if snippet != "" {
			fmt.Printf("   code: %s\n", snippet)
		}
	}
	return 0
}

func runDef(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("def", flag.ContinueOnError)
	cc := bindCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	symbol := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if symbol == "" {
		return fail(errors.New("symbol is required"))
	}
	if err := maybeAutoReindex(ctx, cc); err != nil {
		return fail(err)
	}

	adapter, unit, _, st, err := openUnitStore(cc)
	if err != nil {
		return fail(err)
	}
	defer st.Close()

	router := queryrouter.NewRouter(st, embed.NoopProvider{})
	result, err := router.Definition(ctx, unit.RootPath, symbol)
	if err != nil {
		return fail(err)
	}
	printFreshnessWarning(ctx, st, adapter, unit, cc)
	if result.Definition == nil {
		if len(result.Candidates) > 0 {
			if cc.jsonOut {
				if cc.explain {
					printJSON(result)
				} else {
					printJSON(result.Candidates)
				}
				return 1
			}
			fmt.Printf("Ambiguous symbol %q. Candidates:\n", symbol)
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tKIND\tLOCATION")
			for _, candidate := range result.Candidates {
				fmt.Fprintf(
					tw,
					"%s\t%s\t%s:%d:%d\n",
					candidate.DisplayName,
					firstNonEmpty(candidate.Kind, "symbol"),
					candidate.Path,
					candidate.StartLine+1,
					candidate.StartCol+1,
				)
			}
			_ = tw.Flush()
			return 1
		}
		return fail(fmt.Errorf("no definition found for %q", symbol))
	}
	if cc.jsonOut {
		if cc.explain {
			printJSON(result)
		} else {
			printJSON(result.Definition)
		}
		return 0
	}

	if cc.explain {
		fmt.Printf("Route: %s (%s)\n", result.Plan.Mode, result.Plan.Reason)
	}
	fmt.Printf("Definition: %s [%s]\n", result.Definition.DisplayName, result.Definition.Kind)
	fmt.Printf("Location: %s:%d:%d\n", result.Definition.Path, result.Definition.StartLine+1, result.Definition.StartCol+1)
	if result.Definition.DocSummary != "" {
		fmt.Printf("Doc: %s\n", result.Definition.DocSummary)
	}
	return 0
}

func runRefs(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	cc := bindCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	symbol := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if symbol == "" {
		return fail(errors.New("symbol is required"))
	}
	if err := maybeAutoReindex(ctx, cc); err != nil {
		return fail(err)
	}

	adapter, unit, _, st, err := openUnitStore(cc)
	if err != nil {
		return fail(err)
	}
	defer st.Close()

	router := queryrouter.NewRouter(st, embed.NoopProvider{})
	result, err := router.References(ctx, unit.RootPath, symbol, cc.limit)
	if err != nil {
		return fail(err)
	}
	printFreshnessWarning(ctx, st, adapter, unit, cc)
	if result.Definition == nil && len(result.Candidates) > 0 {
		if cc.jsonOut {
			if cc.explain {
				printJSON(result)
			} else {
				printJSON(result.Candidates)
			}
			return 1
		}
		fmt.Printf("Ambiguous symbol %q. Candidates:\n", symbol)
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tKIND\tLOCATION")
		for _, candidate := range result.Candidates {
			fmt.Fprintf(
				tw,
				"%s\t%s\t%s:%d:%d\n",
				candidate.DisplayName,
				firstNonEmpty(candidate.Kind, "symbol"),
				candidate.Path,
				candidate.StartLine+1,
				candidate.StartCol+1,
			)
		}
		_ = tw.Flush()
		return 1
	}
	if cc.jsonOut {
		if cc.explain {
			printJSON(result)
		} else {
			printJSON(result.References)
		}
		return 0
	}
	if len(result.References) == 0 {
		fmt.Println("No references.")
		return 0
	}
	if cc.explain {
		fmt.Printf("Route: %s (%s)\n", result.Plan.Mode, result.Plan.Reason)
	}
	fmt.Printf("References: %d\n", len(result.References))
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "LOCATION\tSYNTAX")
	for _, ref := range result.References {
		fmt.Fprintf(tw, "%s:%d:%d\t%s\n", ref.Path, ref.StartLine+1, ref.StartCol+1, ref.SyntaxKind)
	}
	_ = tw.Flush()
	return 0
}

func runContext(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("context", flag.ContinueOnError)
	cc := bindCommonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		return fail(errors.New("context query is required"))
	}
	if err := maybeAutoReindex(ctx, cc); err != nil {
		return fail(err)
	}

	adapter, unit, _, st, err := openUnitStore(cc)
	if err != nil {
		return fail(err)
	}
	defer st.Close()

	embedder, err := embed.ResolveONNXProvider(unit.RootPath, cc.device)
	if err != nil {
		return fail(err)
	}
	router := queryrouter.NewRouter(st, embedder)
	result, err := router.Context(ctx, unit.RootPath, query, cc.limit, queryrouter.QueryMode(cc.mode))
	if err != nil {
		return fail(err)
	}
	printFreshnessWarning(ctx, st, adapter, unit, cc)
	if result.Seed == nil {
		return fail(fmt.Errorf("no context seeds found for %q", query))
	}
	if cc.jsonOut {
		if cc.explain {
			printJSON(result)
		} else {
			printJSON(map[string]any{
				"definition":      result.Definition,
				"seed":            result.Seed,
				"neighbors":       result.Neighbors,
				"graph_neighbors": result.GraphNeighbors,
				"references":      result.References,
			})
		}
		return 0
	}

	if cc.explain {
		fmt.Printf("Route: %s (%s)\n", result.Plan.Mode, result.Plan.Reason)
	}
	fmt.Printf("Seed: %s:%d-%d [%s] %s\n", result.Seed.Path, result.Seed.StartLine+1, result.Seed.EndLine+1, result.Seed.Kind, firstNonEmpty(result.Seed.Name, result.Seed.HeaderText))
	fmt.Printf("Code:\n%s\n", result.Seed.Text)
	if result.Definition != nil {
		fmt.Printf("Definition: %s:%d:%d [%s]\n", result.Definition.Path, result.Definition.StartLine+1, result.Definition.StartCol+1, result.Definition.Kind)
	}
	if len(result.Neighbors) > 0 {
		fmt.Println("Neighbors:")
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "LOCATION\tKIND\tNAME")
		for _, neighbor := range result.Neighbors {
			fmt.Fprintf(
				tw,
				"%s:%d-%d\t%s\t%s\n",
				neighbor.Path,
				neighbor.StartLine+1,
				neighbor.EndLine+1,
				neighbor.Kind,
				firstNonEmpty(neighbor.Name, neighbor.HeaderText),
			)
		}
		_ = tw.Flush()
	}
	if len(result.GraphNeighbors) > 0 {
		fmt.Println("Graph Neighbors:")
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "DIRECTION\tRELATION\tLOCATION\tKIND\tNAME")
		for _, neighbor := range result.GraphNeighbors {
			fmt.Fprintf(
				tw,
				"%s\t%s\t%s:%d-%d\t%s\t%s\n",
				neighbor.Direction,
				neighbor.RelationKind,
				neighbor.Path,
				neighbor.StartLine+1,
				neighbor.EndLine+1,
				neighbor.Kind,
				firstNonEmpty(neighbor.Name, neighbor.HeaderText),
			)
		}
		_ = tw.Flush()
	}
	if len(result.References) > 0 {
		fmt.Println("References:")
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "LOCATION")
		for _, ref := range result.References {
			fmt.Fprintf(tw, "%s:%d:%d\n", ref.Path, ref.StartLine+1, ref.StartCol+1)
		}
		_ = tw.Flush()
	}
	return 0
}

func bindCommonFlags(fs *flag.FlagSet) *commandContext {
	cc := &commandContext{}
	cwd, _ := os.Getwd()
	fs.StringVar(&cc.rootPath, "root", cwd, "project root or a path inside the project")
	fs.StringVar(&cc.dbPath, "db", "", "override SQLite database path")
	fs.BoolVar(&cc.jsonOut, "json", false, "emit JSON")
	fs.IntVar(&cc.limit, "limit", 10, "result limit")
	fs.BoolVar(&cc.explain, "explain", false, "include routing and freshness details")
	fs.StringVar(&cc.mode, "mode", "auto", "query mode: auto, hybrid, symbol, semantic, graph")
	fs.StringVar(&cc.device, "device", "", "embedding device: cpu, cuda (default: auto-detect)")
	return cc
}

func openUnitStore(cc *commandContext) (indexer.Adapter, workspace.Unit, config.Paths, *store.Store, error) {
	adapter, unit, err := indexer.DetectUnit(adapters(), cc.rootPath)
	if err != nil {
		return nil, workspace.Unit{}, config.Paths{}, nil, err
	}

	paths, err := config.Resolve(unit.RootPath, cc.dbPath)
	if err != nil {
		return nil, workspace.Unit{}, config.Paths{}, nil, err
	}

	st, err := store.Open(paths.DBPath)
	if err != nil {
		return nil, workspace.Unit{}, config.Paths{}, nil, err
	}

	return adapter, unit, paths, st, nil
}

func performIndex(ctx context.Context, cc *commandContext) (map[string]any, string, error) {
	adapter, unit, paths, st, err := openUnitStore(cc)
	if err != nil {
		return nil, "", err
	}
	defer st.Close()

	if err := adapter.Validate(ctx, unit); err != nil {
		return nil, "", err
	}

	artifactPath := filepath.Join(paths.ArtifactDir, "index.scip")
	indexResult, err := adapter.Index(ctx, unit, artifactPath)
	if err != nil {
		return nil, "", err
	}

	index, err := scipgraph.LoadIndex(indexResult.ArtifactPath)
	if err != nil {
		return nil, "", err
	}

	embedder, err := embed.ResolveONNXProvider(unit.RootPath, cc.device)
	if err != nil {
		return nil, "", err
	}

	payload, err := buildPayload(ctx, unit, adapter, index, indexResult, embedder)
	if err != nil {
		return nil, "", err
	}

	if err := st.ReplaceProjectIndex(ctx, payload); err != nil {
		return nil, "", err
	}

	result := map[string]any{
		"project":     unit,
		"artifact":    indexResult.ArtifactPath,
		"files":       len(payload.Files),
		"symbols":     len(payload.Symbols),
		"occurrences": len(payload.Occurrences),
		"chunks":      len(payload.Chunks),
		"edges":       len(payload.Edges),
		"embeddings":  len(payload.Embeddings),
	}

	var out strings.Builder
	out.WriteString("Index Complete\n")
	out.WriteString(fmt.Sprintf("project: %s\n", unit.RootPath))
	out.WriteString(fmt.Sprintf("artifact: %s\n", indexResult.ArtifactPath))
	out.WriteString(fmt.Sprintf("files: %d  symbols: %d  occurrences: %d\n", len(payload.Files), len(payload.Symbols), len(payload.Occurrences)))
	out.WriteString(fmt.Sprintf("chunks: %d  edges: %d  embeddings: %d", len(payload.Chunks), len(payload.Edges), len(payload.Embeddings)))

	if provider, ok := embedder.(embed.DiagnosticsProvider); ok {
		stats := provider.LastStats()
		if stats.Documents > 0 {
			result["embedding_stats"] = stats
			out.WriteString(formatEmbedStats(stats))
		}
	}

	return result, out.String(), nil
}

func maybeAutoReindex(ctx context.Context, cc *commandContext) error {
	adapter, unit, _, st, err := openUnitStore(cc)
	if err != nil {
		return err
	}
	defer st.Close()

	rows, err := st.Status(ctx, unit.RootPath)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		if !cc.jsonOut {
			fmt.Fprintln(os.Stderr, "info: no index found for this project; building index before running the command")
		}
		_, _, indexErr := performIndex(ctx, cc)
		return indexErr
	}

	freshness, err := indexer.ComputeFreshness(ctx, st, adapter, unit)
	if err != nil {
		return nil
	}
	if ok, reason := shouldAutoReindex(freshness); ok {
		if !cc.jsonOut {
			fmt.Fprintf(
				os.Stderr,
				"info: %s; auto re-indexing (changed=%d/%d, dirty=%d new=%d missing=%d)\n",
				reason,
				freshness.ChangedFiles,
				max(freshness.IndexedFiles, freshness.CurrentFiles),
				freshness.DirtyFiles,
				freshness.NewFiles,
				freshness.MissingFiles,
			)
		}
		_, _, indexErr := performIndex(ctx, cc)
		return indexErr
	}
	return nil
}

func shouldAutoReindex(freshness indexer.Freshness) (bool, string) {
	if !freshness.Dirty {
		return false, ""
	}
	if freshness.ChangedFiles >= autoReindexMinChangedFiles && freshness.ChangedRatio >= autoReindexMinChangedRatio {
		return true, fmt.Sprintf("index is stale after a large refactor (>= %d files and >= %.0f%% changed)", autoReindexMinChangedFiles, autoReindexMinChangedRatio*100)
	}
	if freshness.MissingFiles >= autoReindexMissingFileFloor {
		return true, fmt.Sprintf("index is stale with many moved/deleted files (>= %d missing)", autoReindexMissingFileFloor)
	}
	return false, ""
}

func buildPayload(
	ctx context.Context,
	unit workspace.Unit,
	adapter indexer.Adapter,
	index *scip.Index,
	indexResult indexer.Result,
	embedder embed.Provider,
) (store.IndexPayload, error) {
	fileMap := map[string]store.FileData{}
	fileSources := map[string][]byte{}
	symbolMap := map[string]store.SymbolData{}
	var occurrences []store.OccurrenceData
	var chunks []store.ChunkData
	var edges []store.EdgeData
	var defs []defOccurrence

	for _, external := range index.GetExternalSymbols() {
		symbolMap[external.GetSymbol()] = toSymbolData(external, "", adapter.NormalizeDisplayName)
		edges = append(edges, relationshipEdges(external)...)
	}

	for _, doc := range index.GetDocuments() {
		relativePath := filepath.Clean(doc.GetRelativePath())
		absPath := filepath.Join(unit.RootPath, relativePath)
		content, err := os.ReadFile(absPath)
		if err != nil {
			return store.IndexPayload{}, fmt.Errorf("read source file %s: %w", absPath, err)
		}

		fileMap[relativePath] = store.FileData{
			RelativePath: relativePath,
			AbsPath:      absPath,
			Language:     doc.GetLanguage(),
			ContentHash:  sha256Hex(content),
		}
		fileSources[relativePath] = content

		for _, info := range doc.GetSymbols() {
			data := toSymbolData(info, relativePath, adapter.NormalizeDisplayName)
			symbolMap[data.ScipSymbol] = data
			edges = append(edges, relationshipEdges(info)...)
			if data.EnclosingSymbol != "" {
				edges = append(edges, store.EdgeData{
					SrcSymbol:  data.EnclosingSymbol,
					DstSymbol:  data.ScipSymbol,
					Kind:       "contains",
					Provenance: "scip",
				})
			}
		}

		astChunks, err := adapter.SyntaxExtractor().Extract(ctx, relativePath, content)
		if err != nil {
			return store.IndexPayload{}, fmt.Errorf("extract chunks for %s: %w", relativePath, err)
		}

		for _, occ := range doc.GetOccurrences() {
			rng, err := scipgraph.ParseRange(occ.GetRange())
			if err != nil {
				return store.IndexPayload{}, fmt.Errorf("parse occurrence range for %s: %w", relativePath, err)
			}
			enclosing := rng
			if len(occ.GetEnclosingRange()) > 0 {
				if parsed, parseErr := scipgraph.ParseRange(occ.GetEnclosingRange()); parseErr == nil {
					enclosing = parsed
				}
			}

			if occ.GetSymbol() != "" {
				if _, ok := symbolMap[occ.GetSymbol()]; !ok {
					symbolMap[occ.GetSymbol()] = store.SymbolData{
						ScipSymbol:  occ.GetSymbol(),
						DisplayName: adapter.NormalizeDisplayName(occ.GetSymbol()),
						Kind:        "symbol",
					}
				}
			}

			flags := scipgraph.DecodeRoles(occ.GetSymbolRoles())
			occurrence := store.OccurrenceData{
				FilePath:           relativePath,
				Symbol:             occ.GetSymbol(),
				StartLine:          rng.StartLine,
				StartCol:           rng.StartCol,
				EndLine:            rng.EndLine,
				EndCol:             rng.EndCol,
				EnclosingStartLine: enclosing.StartLine,
				EnclosingStartCol:  enclosing.StartCol,
				EnclosingEndLine:   enclosing.EndLine,
				EnclosingEndCol:    enclosing.EndCol,
				RoleBits:           int(occ.GetSymbolRoles()),
				SyntaxKind:         firstNonEmpty(scipgraph.NormalizeSyntaxKind(occ.GetSyntaxKind()), "reference"),
				IsDefinition:       flags.Definition,
				IsImport:           flags.Import,
				IsRead:             flags.Read,
				IsWrite:            flags.Write,
			}
			if occurrence.Symbol != "" {
				occurrences = append(occurrences, occurrence)
			}

			if flags.Definition && occ.GetSymbol() != "" {
				symbolData := symbolMap[occ.GetSymbol()]
				if symbolData.ScipSymbol != "" {
					symbolData.FilePath = relativePath
					symbolData.DefStartLine = enclosing.StartLine
					symbolData.DefStartCol = enclosing.StartCol
					symbolData.DefEndLine = enclosing.EndLine
					symbolData.DefEndCol = enclosing.EndCol
					if symbolData.DisplayName == "" || symbolData.DisplayName == symbolData.ScipSymbol {
						symbolData.DisplayName = adapter.NormalizeDisplayName(occ.GetSymbol())
					}
					symbolMap[occ.GetSymbol()] = symbolData
				}
				defs = append(defs, defOccurrence{
					FilePath: relativePath,
					Symbol:   occ.GetSymbol(),
					Range:    enclosing,
				})
			}
		}

		for _, chunk := range astChunks {
			chunks = append(chunks, store.ChunkData{
				Key:        chunk.Key,
				FilePath:   chunk.FilePath,
				Kind:       chunk.Kind,
				Name:       chunk.Name,
				ParentKey:  chunk.ParentKey,
				StartByte:  chunk.StartByte,
				EndByte:    chunk.EndByte,
				StartLine:  chunk.StartLine,
				StartCol:   chunk.StartCol,
				EndLine:    chunk.EndLine,
				EndCol:     chunk.EndCol,
				Text:       chunk.Text,
				HeaderText: chunk.HeaderText,
			})
		}
	}

	assignPrimarySymbols(chunks, defs, symbolMap, adapter.NormalizeDisplayName)
	for i := range chunks {
		chunks[i].RetrievalText = buildRetrievalText(chunks[i], symbolMap[chunks[i].PrimarySymbol])
	}
	chunkSymbols := deriveChunkSymbolLinks(chunks, occurrences)

	embedDocs := make([]embed.Document, 0, len(chunks))
	for _, chunk := range chunks {
		embedDocs = append(embedDocs, embed.Document{
			OwnerType: "chunk",
			OwnerKey:  chunk.Key,
			Text:      chunk.RetrievalText,
		})
	}
	vectors, err := embedder.Embed(ctx, embedDocs)
	if err != nil {
		return store.IndexPayload{}, err
	}
	vectorByKey := make(map[string][]float32, len(vectors))
	for _, vector := range vectors {
		vectorByKey[embeddingMapKey(vector.OwnerType, vector.OwnerKey)] = vector.Values
	}
	embeddings := make([]store.EmbeddingData, 0, len(embedDocs))
	for _, doc := range embedDocs {
		vector := vectorByKey[embeddingMapKey(doc.OwnerType, doc.OwnerKey)]
		if len(vector) == 0 {
			continue
		}
		embeddings = append(embeddings, store.EmbeddingData{
			OwnerType: doc.OwnerType,
			OwnerKey:  doc.OwnerKey,
			Model:     embedder.Name(),
			TextHash:  sha256Hex([]byte(doc.Text)),
			Text:      doc.Text,
			Vector:    vector,
		})
	}

	files := mapsToSortedFiles(fileMap)
	symbols := mapsToSortedSymbols(symbolMap)
	derivedEdges, err := adapter.DeriveEdges(ctx, indexer.DeriveRequest{
		Unit:        unit,
		FileSources: fileSources,
		Symbols:     symbolMap,
		Occurrences: occurrences,
		Chunks:      chunks,
	})
	if err != nil {
		return store.IndexPayload{}, err
	}
	edges = append(edges, derivedEdges...)

	return store.IndexPayload{
		Project: store.ProjectData{
			RootPath:          unit.RootPath,
			Name:              unit.Name,
			Language:          unit.Language,
			ManifestPath:      unit.ManifestPath,
			EnvironmentSource: unit.EnvironmentSource,
			AdapterID:         unit.AdapterID,
			ScipArtifactPath:  indexResult.ArtifactPath,
			ToolName:          indexResult.ToolName,
			ToolVersion:       indexResult.ToolVersion,
		},
		Files:        files,
		Symbols:      symbols,
		Occurrences:  occurrences,
		Chunks:       chunks,
		ChunkSymbols: chunkSymbols,
		Edges:        dedupeEdges(edges),
		Embeddings:   embeddings,
	}, nil
}

func buildRetrievalText(chunk store.ChunkData, symbol store.SymbolData) string {
	var b strings.Builder
	b.WriteString("file: ")
	b.WriteString(chunk.FilePath)
	b.WriteString("\nkind: ")
	b.WriteString(chunk.Kind)
	if chunk.Name != "" {
		b.WriteString("\nname: ")
		b.WriteString(chunk.Name)
	}
	if symbol.ScipSymbol != "" {
		b.WriteString("\nsymbol: ")
		b.WriteString(symbol.ScipSymbol)
	}
	if symbol.DocSummary != "" {
		b.WriteString("\ndoc: ")
		b.WriteString(symbol.DocSummary)
	}
	b.WriteString("\ncode:\n")
	b.WriteString(chunk.Text)
	return b.String()
}

func formatEmbedStats(stats embed.Stats) string {
	out := fmt.Sprintf(
		"\nembed_provider: %s\nembed_docs: %d\nembed_dim: %d\nembed_batch: requested=%d settled=%d batches=%d oom_retries=%d\nembed_timing_ms: total=%.1f request=%.1f preload=%.1f session=%.1f tokenize=%.1f infer=%.1f normalize=%.1f serialize=%.1f decode=%.1f",
		firstNonEmpty(stats.Provider, "unknown"),
		stats.Documents,
		stats.Dimensions,
		stats.RequestedBatch,
		stats.SelectedBatch,
		stats.BatchCount,
		stats.OOMRetries,
		stats.TotalMS,
		stats.RequestMS,
		stats.PreloadMS,
		stats.SessionMS,
		stats.TokenizeMS,
		stats.InferMS,
		stats.NormalizeMS,
		stats.SerializeMS,
		stats.DecodeMS,
	)
	if len(stats.BatchStats) > 0 {
		out += "\nembed_batch_samples:"
		for _, sample := range sampleBatchStats(stats.BatchStats, 6) {
			out += fmt.Sprintf(
				"\n  #%d size=%d processed=%d tokenize=%.1f infer=%.1f normalize=%.1f retries=%d settled=%d",
				sample.Index,
				sample.Size,
				sample.Processed,
				sample.TokenizeMS,
				sample.InferMS,
				sample.NormalizeMS,
				sample.RetryCount,
				sample.SettledBatch,
			)
		}
	}
	return out
}

func sampleBatchStats(items []embed.BatchStats, maxItems int) []embed.BatchStats {
	if len(items) <= maxItems {
		return items
	}
	keep := maxItems / 2
	if keep < 1 {
		keep = 1
	}
	out := make([]embed.BatchStats, 0, keep*2)
	out = append(out, items[:keep]...)
	out = append(out, items[len(items)-keep:]...)
	return out
}

func buildSymbolRetrievalText(symbol store.SymbolData) string {
	var b strings.Builder
	b.WriteString("symbol: ")
	b.WriteString(symbol.DisplayName)
	if symbol.Kind != "" {
		b.WriteString("\nkind: ")
		b.WriteString(symbol.Kind)
	}
	if symbol.FilePath != "" {
		b.WriteString("\nfile: ")
		b.WriteString(symbol.FilePath)
	}
	if symbol.EnclosingSymbol != "" {
		b.WriteString("\nenclosing: ")
		b.WriteString(symbol.EnclosingSymbol)
	}
	if symbol.Signature != "" {
		b.WriteString("\nsignature: ")
		b.WriteString(symbol.Signature)
	}
	if symbol.DocSummary != "" {
		b.WriteString("\ndoc: ")
		b.WriteString(symbol.DocSummary)
	}
	if symbol.ScipSymbol != "" {
		b.WriteString("\nscip: ")
		b.WriteString(symbol.ScipSymbol)
	}
	return b.String()
}

func deriveChunkSymbolLinks(chunks []store.ChunkData, occurrences []store.OccurrenceData) []store.ChunkSymbolLinkData {
	fileChunks := map[string][]store.ChunkData{}
	for _, chunk := range chunks {
		fileChunks[chunk.FilePath] = append(fileChunks[chunk.FilePath], chunk)
	}

	fileOccurrences := map[string][]store.OccurrenceData{}
	for _, occ := range occurrences {
		if occ.Symbol == "" {
			continue
		}
		fileOccurrences[occ.FilePath] = append(fileOccurrences[occ.FilePath], occ)
	}

	out := make([]store.ChunkSymbolLinkData, 0, len(chunks)*2)
	seen := map[string]struct{}{}
	add := func(chunkKey string, symbol string, role string, weight float64) {
		if chunkKey == "" || symbol == "" || role == "" || weight <= 0 {
			return
		}
		key := chunkKey + "\x00" + symbol + "\x00" + role
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, store.ChunkSymbolLinkData{
			ChunkKey: chunkKey,
			Symbol:   symbol,
			Role:     role,
			Weight:   weight,
		})
	}

	for filePath, chunkGroup := range fileChunks {
		occGroup := fileOccurrences[filePath]
		sort.Slice(occGroup, func(i, j int) bool {
			if occGroup[i].StartLine == occGroup[j].StartLine {
				return occGroup[i].StartCol < occGroup[j].StartCol
			}
			return occGroup[i].StartLine < occGroup[j].StartLine
		})

		for _, chunk := range chunkGroup {
			if chunk.PrimarySymbol != "" {
				add(chunk.Key, chunk.PrimarySymbol, "defines", 1.0)
			}

			start := sort.Search(len(occGroup), func(i int) bool {
				return occGroup[i].StartLine >= chunk.StartLine
			})
			for i := start; i < len(occGroup); i++ {
				occ := occGroup[i]
				if occ.StartLine > chunk.EndLine {
					break
				}
				if !chunkContainsOccurrence(chunk, occ) {
					continue
				}
				addChunkSymbolRoles(add, chunk.Key, occ)
			}
		}
	}

	return out
}

func addChunkSymbolRoles(add func(string, string, string, float64), chunkKey string, occ store.OccurrenceData) {
	if occ.IsDefinition {
		add(chunkKey, occ.Symbol, "defines", 1.0)
	}
	if occ.IsImport {
		add(chunkKey, occ.Symbol, "imports", 0.65)
	}
	if occ.IsWrite {
		add(chunkKey, occ.Symbol, "writes", 0.75)
	}
	if occ.IsRead {
		add(chunkKey, occ.Symbol, "reads", 0.6)
	}
	addedAccess := occ.IsDefinition || occ.IsImport || occ.IsWrite || occ.IsRead
	if !addedAccess || strings.HasPrefix(occ.SyntaxKind, "identifier") {
		add(chunkKey, occ.Symbol, "uses", 0.55)
	}
}

func chunkContainsOccurrence(chunk store.ChunkData, occ store.OccurrenceData) bool {
	if chunk.FilePath != occ.FilePath {
		return false
	}
	if comparePosition(occ.StartLine, occ.StartCol, chunk.StartLine, chunk.StartCol) < 0 {
		return false
	}
	if comparePosition(occ.EndLine, occ.EndCol, chunk.EndLine, chunk.EndCol) > 0 {
		return false
	}
	return true
}

func comparePosition(lineA int, colA int, lineB int, colB int) int {
	if lineA < lineB {
		return -1
	}
	if lineA > lineB {
		return 1
	}
	if colA < colB {
		return -1
	}
	if colA > colB {
		return 1
	}
	return 0
}

func assignPrimarySymbols(
	chunks []store.ChunkData,
	defs []defOccurrence,
	symbolMap map[string]store.SymbolData,
	normalizeDisplayName func(string) string,
) {
	for i := range chunks {
		bestSpan := 0
		bestSymbol := ""
		for _, def := range defs {
			if def.FilePath != chunks[i].FilePath {
				continue
			}
			if def.Range.StartLine < chunks[i].StartLine || def.Range.EndLine > chunks[i].EndLine {
				continue
			}
			displayName := symbolMap[def.Symbol].DisplayName
			if displayName == "" {
				displayName = normalizeDisplayName(def.Symbol)
			}
			if chunks[i].Name != "" && displayName == chunks[i].Name {
				bestSymbol = def.Symbol
				bestSpan = 1
				break
			}

			span := def.Range.EndLine - def.Range.StartLine
			if bestSymbol == "" || span < bestSpan {
				bestSymbol = def.Symbol
				bestSpan = span
			}
		}
		chunks[i].PrimarySymbol = bestSymbol
	}
}

func toSymbolData(
	info *scip.SymbolInformation,
	filePath string,
	normalizeDisplayName func(string) string,
) store.SymbolData {
	data := store.SymbolData{
		ScipSymbol:      info.GetSymbol(),
		DisplayName:     firstNonEmpty(info.GetDisplayName(), normalizeDisplayName(info.GetSymbol())),
		Kind:            firstNonEmpty(scipgraph.NormalizeSymbolKind(info.GetKind()), "symbol"),
		FilePath:        filePath,
		Signature:       "",
		DocSummary:      scipgraph.DocumentationSummary(info.GetDocumentation()),
		EnclosingSymbol: info.GetEnclosingSymbol(),
	}
	return data
}

func relationshipEdges(info *scip.SymbolInformation) []store.EdgeData {
	if info == nil || info.GetSymbol() == "" {
		return nil
	}
	var edges []store.EdgeData
	for _, rel := range info.GetRelationships() {
		if rel == nil || rel.GetSymbol() == "" {
			continue
		}
		if rel.GetIsReference() {
			edges = append(edges, store.EdgeData{
				SrcSymbol:  info.GetSymbol(),
				DstSymbol:  rel.GetSymbol(),
				Kind:       "reference",
				Provenance: "scip",
			})
		}
		if rel.GetIsImplementation() {
			edges = append(edges, store.EdgeData{
				SrcSymbol:  info.GetSymbol(),
				DstSymbol:  rel.GetSymbol(),
				Kind:       "implementation",
				Provenance: "scip",
			})
		}
		if rel.GetIsTypeDefinition() {
			edges = append(edges, store.EdgeData{
				SrcSymbol:  info.GetSymbol(),
				DstSymbol:  rel.GetSymbol(),
				Kind:       "type_definition",
				Provenance: "scip",
			})
		}
		if rel.GetIsDefinition() {
			edges = append(edges, store.EdgeData{
				SrcSymbol:  info.GetSymbol(),
				DstSymbol:  rel.GetSymbol(),
				Kind:       "definition",
				Provenance: "scip",
			})
		}
	}
	return edges
}

func mapsToSortedFiles(fileMap map[string]store.FileData) []store.FileData {
	files := make([]store.FileData, 0, len(fileMap))
	for _, file := range fileMap {
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].RelativePath < files[j].RelativePath
	})
	return files
}

func mapsToSortedSymbols(symbolMap map[string]store.SymbolData) []store.SymbolData {
	symbols := make([]store.SymbolData, 0, len(symbolMap))
	for _, symbol := range symbolMap {
		symbols = append(symbols, symbol)
	}
	sort.Slice(symbols, func(i, j int) bool {
		return symbols[i].ScipSymbol < symbols[j].ScipSymbol
	})
	return symbols
}

func dedupeEdges(edges []store.EdgeData) []store.EdgeData {
	seen := map[string]struct{}{}
	out := make([]store.EdgeData, 0, len(edges))
	for _, edge := range edges {
		if edge.SrcSymbol == "" || edge.DstSymbol == "" {
			continue
		}
		key := edge.SrcSymbol + "\x00" + edge.DstSymbol + "\x00" + edge.Kind + "\x00" + edge.Provenance
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, edge)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SrcSymbol == out[j].SrcSymbol {
			if out[i].DstSymbol == out[j].DstSymbol {
				return out[i].Kind < out[j].Kind
			}
			return out[i].DstSymbol < out[j].DstSymbol
		}
		return out[i].SrcSymbol < out[j].SrcSymbol
	})
	return out
}

func embeddingMapKey(ownerType string, ownerKey string) string {
	return ownerType + "\x00" + ownerKey
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func truncate(value string, n int) string {
	value = strings.TrimSpace(value)
	if len(value) <= n {
		return value
	}
	return value[:n] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func printUsage() {
	fmt.Print(`wave <command> [flags]

Commands:
  index     Index the current project with SCIP + Tree-sitter
  status    Show indexed project status
  search    Search indexed symbols/chunks
  def       Resolve a symbol definition
  refs      List symbol references
  context   Build a small contextual bundle around a search hit
`)
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, err)
	return 1
}

func printResult(jsonOut bool, payload any, text string) {
	if jsonOut {
		printJSON(payload)
		return
	}
	fmt.Println(text)
}

func printJSON(payload any) {
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode json: %v\n", err)
		return
	}
	fmt.Println(string(encoded))
}

func adapterByID(id string) indexer.Adapter {
	for _, adapter := range adapters() {
		if adapter.ID() == id {
			return adapter
		}
	}
	return nil
}

func printFreshnessWarning(ctx context.Context, st *store.Store, adapter indexer.Adapter, unit workspace.Unit, cc *commandContext) {
	if adapter == nil {
		return
	}
	freshness, err := indexer.ComputeFreshness(ctx, st, adapter, unit)
	if err != nil || !freshness.Dirty || cc.jsonOut {
		return
	}
	fmt.Fprintf(os.Stderr, "warning: index is %s; dirty=%d new=%d missing=%d\n", freshness.Status, freshness.DirtyFiles, freshness.NewFiles, freshness.MissingFiles)
}
