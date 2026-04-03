package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/1001encore/wave/internal/config"
	"github.com/1001encore/wave/internal/embed"
	scip "github.com/1001encore/wave/internal/gen/scippb"
	"github.com/1001encore/wave/internal/golang"
	"github.com/1001encore/wave/internal/indexer"
	"github.com/1001encore/wave/internal/java"
	"github.com/1001encore/wave/internal/python"
	queryrouter "github.com/1001encore/wave/internal/query"
	"github.com/1001encore/wave/internal/rust"
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

type indexUnitSummary struct {
	Language    string
	AdapterID   string
	Artifact    string
	Files       int
	Symbols     int
	Occurrences int
	Chunks      int
	Edges       int
	Embeddings  int
}

type indexTotals struct {
	Files       int
	Symbols     int
	Occurrences int
	Chunks      int
	Edges       int
	Embeddings  int
}

type indexerInstallSpec struct {
	Binary             string
	Method             string
	NPMPackage         string
	GoModule           string
	RustupComponent    string
	CoursierCoordinate string
}

const (
	autoReindexMinChangedFiles  = 24
	autoReindexMinChangedRatio  = 0.18
	autoReindexMissingFileFloor = 8
	defaultResultLimit          = 10
	defaultDefLimit             = 3
	defaultRefsLimit            = 3
)

func adapters() []indexer.Adapter {
	return []indexer.Adapter{
		golang.Adapter{},
		java.Adapter{},
		python.Adapter{},
		rust.Adapter{},
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
	detectedRoot, _, detectErr := detectWorkspaceUnits(root)
	if detectErr == nil && detectedRoot != "" {
		root = detectedRoot
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
	if detectErr == nil && detectedRoot != "" {
		rows, err = st.Status(ctx, detectedRoot)
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
	var showScore bool
	var showSoftmax bool
	fs.BoolVar(&showScore, "show-score", false, "include raw rerank scores in output")
	fs.BoolVar(&showSoftmax, "show-softmax", false, "include softmax probabilities over returned hits (relative within this result set)")
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

	rootPath, units, _, st, err := openWorkspaceStore(cc)
	if err != nil {
		return fail(err)
	}
	defer st.Close()

	embedder, err := embed.ResolveONNXProvider(rootPath, cc.device)
	if err != nil {
		return fail(err)
	}
	router := queryrouter.NewRouter(st, embedder)
	result, err := router.Search(ctx, rootPath, query, cc.limit, queryrouter.QueryMode(cc.mode))
	if err != nil {
		return fail(err)
	}
	printFreshnessWarning(ctx, st, units, cc)
	if cc.jsonOut {
		printJSON(searchJSONPayload(result, showScore, showSoftmax, cc.explain))
		return 0
	}

	if cc.explain {
		fmt.Printf("Route: %s (%s)\n", result.Plan.Mode, result.Plan.Reason)
	}
	if len(result.Hits) == 0 {
		fmt.Println("No matches.")
		return 0
	}
	var softmax []float64
	if showSoftmax {
		softmax = outputSoftmaxProbabilities(result.Hits)
	}
	fmt.Printf("Matches: %d\n", len(result.Hits))
	for i, hit := range result.Hits {
		summary := firstNonEmpty(hit.Name, hit.HeaderText)
		if showScore || showSoftmax {
			parts := make([]string, 0, 2)
			if showScore {
				parts = append(parts, fmt.Sprintf("score=%.3f", hit.Score))
			}
			if showSoftmax {
				parts = append(parts, fmt.Sprintf("softmax=%.1f%%", softmax[i]*100))
			}
			summary = fmt.Sprintf("%s  [%s]", summary, strings.Join(parts, ", "))
		}
		fmt.Printf(
			"%d. %s:%d-%d  [%s]  %s\n",
			i+1,
			hit.Path,
			hit.StartLine+1,
			hit.EndLine+1,
			hit.Kind,
			summary,
		)
		snippet := truncate(hit.HeaderText, 200)
		if snippet != "" {
			fmt.Printf("   code: %s\n", snippet)
		}
	}
	return 0
}

func runDef(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("def", flag.ContinueOnError)
	cc := bindCommonFlagsWithLimit(fs, defaultDefLimit)
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

	rootPath, units, _, st, err := openWorkspaceStore(cc)
	if err != nil {
		return fail(err)
	}
	defer st.Close()

	router := queryrouter.NewRouter(st, embed.NoopProvider{})
	result, err := router.Definition(ctx, rootPath, symbol)
	if err != nil {
		return fail(err)
	}
	printFreshnessWarning(ctx, st, units, cc)
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
		printJSON(definitionJSONPayload(result, cc.explain))
		return 0
	}
	writeDefinitionOutput(os.Stdout, symbol, result, cc.explain, cc.limit)
	return 0
}

func runRefs(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	cc := bindCommonFlagsWithLimit(fs, defaultRefsLimit)
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

	rootPath, units, _, st, err := openWorkspaceStore(cc)
	if err != nil {
		return fail(err)
	}
	defer st.Close()

	router := queryrouter.NewRouter(st, embed.NoopProvider{})
	result, err := router.References(ctx, rootPath, symbol, cc.limit)
	if err != nil {
		return fail(err)
	}
	printFreshnessWarning(ctx, st, units, cc)
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

	rootPath, units, _, st, err := openWorkspaceStore(cc)
	if err != nil {
		return fail(err)
	}
	defer st.Close()

	embedder, err := embed.ResolveONNXProvider(rootPath, cc.device)
	if err != nil {
		return fail(err)
	}
	router := queryrouter.NewRouter(st, embedder)
	result, err := router.Context(ctx, rootPath, query, cc.limit, queryrouter.QueryMode(cc.mode))
	if err != nil {
		return fail(err)
	}
	printFreshnessWarning(ctx, st, units, cc)
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
	return bindCommonFlagsWithLimit(fs, defaultResultLimit)
}

func bindCommonFlagsWithLimit(fs *flag.FlagSet, defaultLimit int) *commandContext {
	cc := &commandContext{}
	cwd, _ := os.Getwd()
	fs.StringVar(&cc.rootPath, "root", cwd, "project root or a path inside the project")
	fs.StringVar(&cc.dbPath, "db", "", "override SQLite database path")
	fs.BoolVar(&cc.jsonOut, "json", false, "emit JSON")
	fs.IntVar(&cc.limit, "limit", defaultLimit, "result limit")
	fs.BoolVar(&cc.explain, "explain", false, "include routing and freshness details")
	fs.StringVar(&cc.mode, "mode", "auto", "query mode: auto, hybrid, symbol, semantic, graph")
	fs.StringVar(&cc.device, "device", "cuda", "embedding device: cpu, cuda (default: cuda)")
	return cc
}

func detectWorkspaceUnits(start string) (string, []indexer.DetectedUnit, error) {
	detected, err := indexer.DetectUnits(adapters(), start)
	if err != nil {
		return "", nil, err
	}
	if len(detected) == 0 {
		return "", nil, fmt.Errorf("no supported workspace unit found from %s", start)
	}

	rootCounts := map[string]int{}
	for _, item := range detected {
		rootCounts[item.Unit.RootPath]++
	}
	bestRoot := detected[0].Unit.RootPath
	bestCount := rootCounts[bestRoot]
	for _, item := range detected[1:] {
		root := item.Unit.RootPath
		count := rootCounts[root]
		if count > bestCount || (count == bestCount && len(root) > len(bestRoot)) {
			bestRoot = root
			bestCount = count
		}
	}

	selected := make([]indexer.DetectedUnit, 0, bestCount)
	for _, item := range detected {
		if item.Unit.RootPath == bestRoot {
			selected = append(selected, item)
		}
	}
	if len(selected) == 0 {
		return "", nil, fmt.Errorf("no supported workspace unit found from %s", start)
	}
	return bestRoot, selected, nil
}

func openWorkspaceStore(cc *commandContext) (string, []indexer.DetectedUnit, config.Paths, *store.Store, error) {
	rootPath, units, err := detectWorkspaceUnits(cc.rootPath)
	if err != nil {
		return "", nil, config.Paths{}, nil, err
	}

	paths, err := config.Resolve(rootPath, cc.dbPath)
	if err != nil {
		return "", nil, config.Paths{}, nil, err
	}

	st, err := store.Open(paths.DBPath)
	if err != nil {
		return "", nil, config.Paths{}, nil, err
	}

	return rootPath, units, paths, st, nil
}

func artifactSuffix(adapterID string) string {
	adapterID = strings.TrimSpace(strings.ToLower(adapterID))
	if adapterID == "" {
		return "workspace"
	}
	var b strings.Builder
	for _, r := range adapterID {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	value := strings.Trim(b.String(), "_")
	if value == "" {
		return "workspace"
	}
	return value
}

func indexerInstallSpecForAdapter(adapterID string) (indexerInstallSpec, bool) {
	switch strings.TrimSpace(strings.ToLower(adapterID)) {
	case "go-scip":
		return indexerInstallSpec{
			Binary:   "scip-go",
			Method:   "go-install",
			GoModule: "github.com/sourcegraph/scip-go/cmd/scip-go@latest",
		}, true
	case "java-scip":
		return indexerInstallSpec{
			Binary:             "scip-java",
			Method:             "coursier-bootstrap",
			CoursierCoordinate: "com.sourcegraph:scip-java_2.13:latest.release",
		}, true
	case "python-scip":
		return indexerInstallSpec{
			Binary:     "scip-python",
			Method:     "npm",
			NPMPackage: "@sourcegraph/scip-python",
		}, true
	case "rust-scip":
		return indexerInstallSpec{
			Binary:          "rust-analyzer",
			Method:          "rustup-component",
			RustupComponent: "rust-analyzer",
		}, true
	case "typescript-scip":
		return indexerInstallSpec{
			Binary:     "scip-typescript",
			Method:     "npm",
			NPMPackage: "@sourcegraph/scip-typescript",
		}, true
	default:
		return indexerInstallSpec{}, false
	}
}

func ensureIndexerDependencies(ctx context.Context, units []indexer.DetectedUnit, jsonOut bool) error {
	seen := map[string]struct{}{}
	plans := make([]indexerInstallSpec, 0, len(units))
	for _, item := range units {
		spec, ok := indexerInstallSpecForAdapter(item.Adapter.ID())
		if !ok {
			continue
		}
		key := spec.Binary + "\x00" + spec.NPMPackage
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		plans = append(plans, spec)
	}

	for _, plan := range plans {
		if binaryInstalledAndUsable(ctx, plan) {
			continue
		}
		if !jsonOut {
			fmt.Fprintf(os.Stderr, "info: %s not found; installing via %s\n", plan.Binary, plan.Method)
		}
		var err error
		switch plan.Method {
		case "npm":
			err = installIndexerWithNPM(ctx, plan)
		case "go-install":
			err = installIndexerWithGo(ctx, plan)
		case "rustup-component":
			err = installIndexerWithRustup(ctx, plan)
		case "coursier-bootstrap":
			err = installIndexerWithCoursier(ctx, plan)
		default:
			err = fmt.Errorf("unsupported install method %q", plan.Method)
		}
		if err != nil {
			return err
		}

		if _, err := exec.LookPath(plan.Binary); err != nil {
			return fmt.Errorf("%s still not found on PATH after installation (method=%s)", plan.Binary, plan.Method)
		}
	}
	return nil
}

func binaryInstalledAndUsable(ctx context.Context, plan indexerInstallSpec) bool {
	if _, err := exec.LookPath(plan.Binary); err != nil {
		return false
	}
	if plan.Method != "rustup-component" {
		return true
	}

	cmd := exec.CommandContext(ctx, plan.Binary, "--version")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func installIndexerWithNPM(ctx context.Context, plan indexerInstallSpec) error {
	if _, err := exec.LookPath("npm"); err != nil {
		return fmt.Errorf("%s is required but not found, and npm is unavailable to install it: %w", plan.Binary, err)
	}
	cmd := exec.CommandContext(ctx, "npm", "install", "-g", plan.NPMPackage)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"install %s (%s): %w\n%s",
			plan.Binary,
			plan.NPMPackage,
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return nil
}

func installIndexerWithGo(ctx context.Context, plan indexerInstallSpec) error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("%s is required but not found, and go is unavailable to install it: %w", plan.Binary, err)
	}

	cmd := exec.CommandContext(ctx, "go", "install", plan.GoModule)
	if binDir, err := preferredUserBinDir(); err == nil && binDir != "" {
		if mkErr := os.MkdirAll(binDir, 0o755); mkErr == nil {
			cmd.Env = append(os.Environ(), "GOBIN="+binDir)
			prependPath(binDir)
		}
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"install %s (%s): %w\n%s",
			plan.Binary,
			plan.GoModule,
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return nil
}

func installIndexerWithRustup(ctx context.Context, plan indexerInstallSpec) error {
	if _, err := exec.LookPath("rustup"); err != nil {
		return fmt.Errorf("%s is required but not found, and rustup is unavailable to install it: %w", plan.Binary, err)
	}

	cmd := exec.CommandContext(ctx, "rustup", "component", "add", plan.RustupComponent)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"install %s (rustup component %s): %w\n%s",
			plan.Binary,
			plan.RustupComponent,
			err,
			strings.TrimSpace(string(output)),
		)
	}

	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		prependPath(filepath.Join(home, ".cargo", "bin"))
	}
	return nil
}

func installIndexerWithCoursier(ctx context.Context, plan indexerInstallSpec) error {
	coursierBin, err := resolveCoursierBinary()
	if err != nil {
		return fmt.Errorf(
			"%s is required but not found, and coursier is unavailable to install it: %w",
			plan.Binary,
			err,
		)
	}
	binDir, err := preferredUserBinDir()
	if err != nil {
		return fmt.Errorf("resolve install dir for %s: %w", plan.Binary, err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create install dir %s: %w", binDir, err)
	}
	binaryName := plan.Binary
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	destination := filepath.Join(binDir, binaryName)
	cmd := exec.CommandContext(
		ctx,
		coursierBin,
		"bootstrap",
		"--standalone",
		"-o",
		destination,
		plan.CoursierCoordinate,
		"--main",
		"com.sourcegraph.scip_java.ScipJava",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"install %s (%s): %w\n%s",
			plan.Binary,
			plan.CoursierCoordinate,
			err,
			strings.TrimSpace(string(output)),
		)
	}
	prependPath(binDir)
	return nil
}

func resolveCoursierBinary() (string, error) {
	for _, candidate := range []string{"cs", "coursier"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("neither %q nor %q is available on PATH", "cs", "coursier")
}

func preferredUserBinDir() (string, error) {
	if runtime.GOOS == "windows" {
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			return filepath.Join(local, "wave", "bin"), nil
		}
		if profile := strings.TrimSpace(os.Getenv("USERPROFILE")); profile != "" {
			return filepath.Join(profile, "AppData", "Local", "wave", "bin"), nil
		}
		return "", fmt.Errorf("LOCALAPPDATA is not set")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin"), nil
}

func prependPath(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	current := os.Getenv("PATH")
	sep := string(os.PathListSeparator)
	parts := strings.Split(current, sep)
	for _, part := range parts {
		if filepath.Clean(part) == filepath.Clean(dir) {
			return
		}
	}
	if current == "" {
		_ = os.Setenv("PATH", dir)
		return
	}
	_ = os.Setenv("PATH", dir+sep+current)
}

func performIndex(ctx context.Context, cc *commandContext) (map[string]any, string, error) {
	rootPath, units, paths, st, err := openWorkspaceStore(cc)
	if err != nil {
		return nil, "", err
	}
	defer st.Close()

	if err := ensureIndexerDependencies(ctx, units, cc.jsonOut); err != nil {
		return nil, "", err
	}

	embedder, err := embed.ResolveONNXProvider(rootPath, cc.device)
	if err != nil {
		return nil, "", err
	}

	perUnit := make([]map[string]any, 0, len(units))
	adapterIDs := make([]string, 0, len(units))
	seenAdapter := map[string]struct{}{}
	totals := indexTotals{}
	unitSummaries := make([]indexUnitSummary, 0, len(units))

	for _, item := range units {
		adapter := item.Adapter
		unit := item.Unit

		if err := adapter.Validate(ctx, unit); err != nil {
			return nil, "", err
		}

		artifactPath := filepath.Join(paths.ArtifactDir, fmt.Sprintf("index.%s.scip", artifactSuffix(adapter.ID())))
		indexResult, err := adapter.Index(ctx, unit, artifactPath)
		if err != nil {
			return nil, "", err
		}

		index, err := scipgraph.LoadIndex(indexResult.ArtifactPath)
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

		if _, ok := seenAdapter[adapter.ID()]; !ok {
			seenAdapter[adapter.ID()] = struct{}{}
			adapterIDs = append(adapterIDs, adapter.ID())
		}

		files := len(payload.Files)
		symbols := len(payload.Symbols)
		occurrences := len(payload.Occurrences)
		chunks := len(payload.Chunks)
		edges := len(payload.Edges)
		embeddings := len(payload.Embeddings)

		totals.Files += files
		totals.Symbols += symbols
		totals.Occurrences += occurrences
		totals.Chunks += chunks
		totals.Edges += edges
		totals.Embeddings += embeddings

		perUnit = append(perUnit, map[string]any{
			"unit":        unit,
			"artifact":    indexResult.ArtifactPath,
			"files":       files,
			"symbols":     symbols,
			"occurrences": occurrences,
			"chunks":      chunks,
			"edges":       edges,
			"embeddings":  embeddings,
		})
		unitSummaries = append(unitSummaries, indexUnitSummary{
			Language:    unit.Language,
			AdapterID:   adapter.ID(),
			Artifact:    indexResult.ArtifactPath,
			Files:       files,
			Symbols:     symbols,
			Occurrences: occurrences,
			Chunks:      chunks,
			Edges:       edges,
			Embeddings:  embeddings,
		})
	}

	if err := st.DeleteProjectsExceptAdapters(ctx, rootPath, adapterIDs); err != nil {
		return nil, "", err
	}

	result := map[string]any{
		"workspace":   rootPath,
		"units":       perUnit,
		"files":       totals.Files,
		"symbols":     totals.Symbols,
		"occurrences": totals.Occurrences,
		"chunks":      totals.Chunks,
		"edges":       totals.Edges,
		"embeddings":  totals.Embeddings,
	}
	var embedStats *embed.Stats
	if provider, ok := embedder.(embed.DiagnosticsProvider); ok {
		stats := provider.LastStats()
		if stats.Documents > 0 {
			result["embedding_stats"] = stats
			embedStats = &stats
		}
	}

	return result, formatIndexSummary(rootPath, unitSummaries, totals, embedStats), nil
}

func maybeAutoReindex(ctx context.Context, cc *commandContext) error {
	rootPath, units, _, st, err := openWorkspaceStore(cc)
	if err != nil {
		return err
	}
	defer st.Close()

	rows, err := st.Status(ctx, rootPath)
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

	indexedByAdapter := make(map[string]store.StatusRow, len(rows))
	for _, row := range rows {
		indexedByAdapter[row.AdapterID] = row
	}

	detectedByAdapter := make(map[string]struct{}, len(units))
	for _, item := range units {
		detectedByAdapter[item.Adapter.ID()] = struct{}{}
		if _, ok := indexedByAdapter[item.Adapter.ID()]; !ok {
			if !cc.jsonOut {
				fmt.Fprintf(os.Stderr, "info: missing %s index for this workspace; building index\n", item.Unit.Language)
			}
			_, _, indexErr := performIndex(ctx, cc)
			return indexErr
		}

		freshness, err := indexer.ComputeFreshness(ctx, st, item.Adapter, item.Unit)
		if err != nil {
			continue
		}
		if ok, reason := shouldAutoReindex(freshness); ok {
			if !cc.jsonOut {
				fmt.Fprintf(
					os.Stderr,
					"info: %s (%s); auto re-indexing (changed=%d/%d, dirty=%d new=%d missing=%d)\n",
					reason,
					item.Unit.Language,
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
	}

	for _, row := range rows {
		if _, ok := detectedByAdapter[row.AdapterID]; ok {
			continue
		}
		if !cc.jsonOut {
			fmt.Fprintf(os.Stderr, "info: adapter %s is no longer detected in this workspace; refreshing index\n", firstNonEmpty(row.AdapterID, "unknown"))
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
	rootPath := filepath.Clean(unit.RootPath)
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
		relativePath, absPath, ok := resolveDocumentPath(rootPath, doc.GetRelativePath())
		if !ok {
			continue
		}
		content, err := os.ReadFile(absPath)
		if err != nil {
			return store.IndexPayload{}, fmt.Errorf("read source file %s: %w", absPath, err)
		}

		fileMap[relativePath] = store.FileData{
			RelativePath: relativePath,
			AbsPath:      absPath,
			Language:     firstNonEmpty(unit.Language, doc.GetLanguage()),
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

func resolveDocumentPath(rootPath string, rawRelativePath string) (string, string, bool) {
	relativePath := normalizeRelativePath(rawRelativePath)
	if relativePath == "" {
		return "", "", false
	}

	absPath := filepath.Clean(filepath.Join(rootPath, filepath.FromSlash(relativePath)))
	if !pathWithinRoot(rootPath, absPath) {
		return "", "", false
	}
	return relativePath, absPath, true
}

func normalizeRelativePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	path = strings.TrimPrefix(path, "./")
	if path == "." {
		return ""
	}
	return path
}

func pathWithinRoot(rootPath string, candidatePath string) bool {
	root := filepath.Clean(rootPath)
	candidate := filepath.Clean(candidatePath)
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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

func formatIndexSummary(rootPath string, units []indexUnitSummary, totals indexTotals, stats *embed.Stats) string {
	var out bytes.Buffer
	out.WriteString("Index Complete\n")
	out.WriteString(fmt.Sprintf("Workspace: %s\n", rootPath))
	out.WriteString(fmt.Sprintf("Languages Indexed: %d\n", len(units)))

	if len(units) > 0 {
		out.WriteString("\n")
		tw := tabwriter.NewWriter(&out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "LANG\tADAPTER\tFILES\tSYMBOLS\tOCCURRENCES\tCHUNKS\tEDGES\tEMBEDS\tARTIFACT")
		for _, item := range units {
			fmt.Fprintf(
				tw,
				"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				item.Language,
				item.AdapterID,
				formatInt(item.Files),
				formatInt(item.Symbols),
				formatInt(item.Occurrences),
				formatInt(item.Chunks),
				formatInt(item.Edges),
				formatInt(item.Embeddings),
				item.Artifact,
			)
		}
		_ = tw.Flush()
	}

	out.WriteString("\nTotals\n")
	out.WriteString(fmt.Sprintf("  files=%s  symbols=%s  occurrences=%s  chunks=%s  edges=%s  embeddings=%s\n",
		formatInt(totals.Files),
		formatInt(totals.Symbols),
		formatInt(totals.Occurrences),
		formatInt(totals.Chunks),
		formatInt(totals.Edges),
		formatInt(totals.Embeddings),
	))

	if stats != nil {
		out.WriteString("\n")
		out.WriteString(formatEmbedStats(*stats))
	}

	return strings.TrimRight(out.String(), "\n")
}

func formatEmbedStats(stats embed.Stats) string {
	var out bytes.Buffer
	out.WriteString("Embeddings\n")
	out.WriteString(fmt.Sprintf("  provider: %s\n", firstNonEmpty(stats.Provider, "unknown")))
	out.WriteString(fmt.Sprintf("  docs: %s  dim: %d\n", formatInt(stats.Documents), stats.Dimensions))
	out.WriteString(fmt.Sprintf(
		"  batch: requested=%s  settled=%d  batches=%d  oom_retries=%d\n",
		requestedBatchLabel(stats.RequestedBatch),
		stats.SelectedBatch,
		stats.BatchCount,
		stats.OOMRetries,
	))
	out.WriteString("  timing_ms:\n")
	out.WriteString(fmt.Sprintf("    total=%.1f  request=%.1f  preload=%.1f  session=%.1f\n", stats.TotalMS, stats.RequestMS, stats.PreloadMS, stats.SessionMS))
	out.WriteString(fmt.Sprintf("    tokenize=%.1f  infer=%.1f  normalize=%.1f  serialize=%.1f  decode=%.1f\n", stats.TokenizeMS, stats.InferMS, stats.NormalizeMS, stats.SerializeMS, stats.DecodeMS))

	if len(stats.BatchStats) > 0 {
		out.WriteString("  batch_samples:\n")
		tw := tabwriter.NewWriter(&out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "    #\tSIZE\tPROCESSED\tTOKENIZE_MS\tINFER_MS\tNORMALIZE_MS\tRETRIES\tSETTLED")
		for _, sample := range sampleBatchStats(stats.BatchStats, 6) {
			fmt.Fprintf(
				tw,
				"    %d\t%d\t%d\t%.1f\t%.1f\t%.1f\t%d\t%d\n",
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
		_ = tw.Flush()
	}
	return strings.TrimRight(out.String(), "\n")
}

func requestedBatchLabel(value int) string {
	if value <= 0 {
		return "auto"
	}
	return strconv.Itoa(value)
}

func formatInt(value int) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	text := strconv.Itoa(value)
	if len(text) <= 3 {
		return sign + text
	}
	var b strings.Builder
	b.Grow(len(text) + len(text)/3)
	rem := len(text) % 3
	if rem == 0 {
		rem = 3
	}
	b.WriteString(text[:rem])
	for i := rem; i < len(text); i += 3 {
		b.WriteByte(',')
		b.WriteString(text[i : i+3])
	}
	return sign + b.String()
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
  index     Index the current project
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

type searchHitOutput struct {
	ChunkID            int64    `json:"chunk_id"`
	FileID             int64    `json:"file_id"`
	PrimarySymbolID    int64    `json:"primary_symbol_id"`
	Path               string   `json:"path"`
	StartLine          int      `json:"start_line"`
	EndLine            int      `json:"end_line"`
	Kind               string   `json:"kind"`
	Name               string   `json:"name"`
	HeaderText         string   `json:"header_text"`
	Text               string   `json:"text"`
	Score              *float64 `json:"score,omitempty"`
	SoftmaxProbability *float64 `json:"softmax_probability,omitempty"`
}

type searchResultOutput struct {
	Plan queryrouter.Plan  `json:"plan"`
	Hits []searchHitOutput `json:"hits"`
}

func searchJSONPayload(result queryrouter.SearchResult, includeScore bool, includeSoftmax bool, explain bool) any {
	hits := searchHitsOutput(result.Hits, includeScore, includeSoftmax)
	if explain {
		return searchResultOutput{
			Plan: result.Plan,
			Hits: hits,
		}
	}
	return hits
}

func searchHitsOutput(hits []store.SearchHit, includeScore bool, includeSoftmax bool) []searchHitOutput {
	out := make([]searchHitOutput, 0, len(hits))
	var softmax []float64
	if includeSoftmax {
		softmax = outputSoftmaxProbabilities(hits)
	}
	for i, hit := range hits {
		item := searchHitOutput{
			ChunkID:         hit.ChunkID,
			FileID:          hit.FileID,
			PrimarySymbolID: hit.PrimarySymbolID,
			Path:            hit.Path,
			StartLine:       hit.StartLine,
			EndLine:         hit.EndLine,
			Kind:            hit.Kind,
			Name:            hit.Name,
			HeaderText:      hit.HeaderText,
			Text:            hit.Text,
		}
		if includeScore {
			score := hit.Score
			item.Score = &score
		}
		if includeSoftmax {
			prob := softmax[i]
			item.SoftmaxProbability = &prob
		}
		out = append(out, item)
	}
	return out
}

func softmaxProbabilities(hits []store.SearchHit) []float64 {
	if len(hits) == 0 {
		return nil
	}
	maxScore := hits[0].Score
	for _, hit := range hits[1:] {
		if hit.Score > maxScore {
			maxScore = hit.Score
		}
	}

	weights := make([]float64, len(hits))
	total := 0.0
	for i, hit := range hits {
		w := math.Exp(hit.Score - maxScore)
		if math.IsNaN(w) || math.IsInf(w, 0) {
			w = 0
		}
		weights[i] = w
		total += w
	}
	if total <= 0 {
		uniform := 1.0 / float64(len(weights))
		for i := range weights {
			weights[i] = uniform
		}
		return weights
	}
	for i := range weights {
		weights[i] /= total
	}
	return weights
}

func outputSoftmaxProbabilities(hits []store.SearchHit) []float64 {
	if len(hits) == 0 {
		return nil
	}
	usePrecomputed := true
	for _, hit := range hits {
		if hit.SoftmaxProbability <= 0 {
			usePrecomputed = false
			break
		}
	}
	if usePrecomputed {
		probs := make([]float64, len(hits))
		for i, hit := range hits {
			probs[i] = hit.SoftmaxProbability
		}
		return probs
	}
	return softmaxProbabilities(hits)
}

func adapterByID(id string) indexer.Adapter {
	for _, adapter := range adapters() {
		if adapter.ID() == id {
			return adapter
		}
	}
	return nil
}

func printFreshnessWarning(ctx context.Context, st *store.Store, units []indexer.DetectedUnit, cc *commandContext) {
	if cc.jsonOut {
		return
	}
	for _, item := range units {
		freshness, err := indexer.ComputeFreshness(ctx, st, item.Adapter, item.Unit)
		if err != nil || !freshness.Dirty {
			continue
		}
		fmt.Fprintf(
			os.Stderr,
			"warning: %s index is %s; dirty=%d new=%d missing=%d\n",
			item.Unit.Language,
			freshness.Status,
			freshness.DirtyFiles,
			freshness.NewFiles,
			freshness.MissingFiles,
		)
	}
}

func definitionJSONPayload(result queryrouter.DefinitionResult, explain bool) any {
	if explain || len(result.Candidates) > 0 || result.Definition == nil {
		return result
	}
	return result.Definition
}

func writeDefinitionOutput(w io.Writer, symbol string, result queryrouter.DefinitionResult, explain bool, alternateLimit int) {
	if explain {
		fmt.Fprintf(w, "Route: %s (%s)\n", result.Plan.Mode, result.Plan.Reason)
	}
	printDefinitionEntry(w, "Definition", result.Definition)

	if len(result.Candidates) == 0 {
		return
	}

	alternates := make([]store.DefinitionResult, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		if result.Definition != nil && candidate.SymbolID == result.Definition.SymbolID {
			continue
		}
		alternates = append(alternates, candidate)
	}
	if len(alternates) == 0 {
		return
	}

	fmt.Fprintln(w, "---")
	fmt.Fprintf(w, "Other matches for %q:\n", symbol)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tKIND\tLOCATION")
	limit := len(alternates)
	if alternateLimit > 0 {
		limit = min(limit, alternateLimit)
	}
	for _, candidate := range alternates[:limit] {
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
	if len(alternates) > limit {
		fmt.Fprintf(w, "... and %d more\n", len(alternates)-limit)
	}
}

func printDefinitionEntry(w io.Writer, label string, def *store.DefinitionResult) {
	if def == nil {
		return
	}
	fmt.Fprintf(w, "%s: %s [%s]\n", label, def.DisplayName, def.Kind)
	fmt.Fprintf(w, "Location: %s:%d:%d\n", def.Path, def.StartLine+1, def.StartCol+1)
	if def.DocSummary != "" {
		fmt.Fprintf(w, "Doc: %s\n", def.DocSummary)
	}
}
