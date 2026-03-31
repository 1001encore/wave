package query

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"wave/internal/embed"
	"wave/internal/store"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_./:#()\-]*$`)

type QueryMode string

const (
	QueryModeAuto     QueryMode = "auto"
	QueryModeHybrid   QueryMode = "hybrid"
	QueryModeSymbol   QueryMode = "symbol"
	QueryModeSemantic QueryMode = "semantic"
	QueryModeGraph    QueryMode = "graph"
)

type Router struct {
	store    *store.Store
	embedder embed.Provider
}

type Plan struct {
	Mode   string `json:"mode"`
	Reason string `json:"reason"`
}

type SearchResult struct {
	Plan Plan              `json:"plan"`
	Hits []store.SearchHit `json:"hits"`
}

type DefinitionResult struct {
	Plan       Plan                    `json:"plan"`
	Definition *store.DefinitionResult `json:"definition,omitempty"`
	Chunk      *store.SearchHit        `json:"chunk,omitempty"`
}

type ReferencesResult struct {
	Plan       Plan                    `json:"plan"`
	Definition *store.DefinitionResult `json:"definition,omitempty"`
	References []store.ReferenceResult `json:"references,omitempty"`
}

type ContextResult struct {
	Plan           Plan                    `json:"plan"`
	Definition     *store.DefinitionResult `json:"definition,omitempty"`
	Seed           *store.SearchHit        `json:"seed,omitempty"`
	Neighbors      []store.SearchHit       `json:"neighbors,omitempty"`
	GraphNeighbors []store.RelatedChunk    `json:"graph_neighbors,omitempty"`
	References     []store.ReferenceResult `json:"references,omitempty"`
}

type querySignals struct {
	exactDef        *store.DefinitionResult
	lexicalSymbols  []store.SymbolSearchHit
	semanticSymbols []store.SymbolSearchHit
	lexicalChunks   []store.SearchHit
	semanticChunks  []store.SearchHit
}

type searchCandidate struct {
	hit   store.SearchHit
	score float64
}

func NewRouter(st *store.Store, embedder embed.Provider) *Router {
	return &Router{
		store:    st,
		embedder: embedder,
	}
}

func (r *Router) Search(ctx context.Context, rootPath string, query string, limit int, mode QueryMode) (SearchResult, error) {
	mode, err := normalizeMode(mode)
	if err != nil {
		return SearchResult{}, err
	}

	switch mode {
	case QueryModeSymbol:
		return r.runSearch(ctx, rootPath, query, limit, mode, "symbol-centric retrieval was explicitly requested", searchOptions{
			exactSymbols:   true,
			lexicalSymbols: true,
			semantic:       true,
			graphExpand:    false,
			lexicalChunks:  false,
		})
	case QueryModeSemantic:
		return r.runSearch(ctx, rootPath, query, limit, mode, "semantic retrieval was explicitly requested", searchOptions{
			exactSymbols:   false,
			lexicalSymbols: false,
			semantic:       true,
			graphExpand:    false,
			lexicalChunks:  false,
		})
	case QueryModeGraph:
		return r.runSearch(ctx, rootPath, query, limit, mode, "graph retrieval was explicitly requested", searchOptions{
			exactSymbols:   true,
			lexicalSymbols: true,
			semantic:       true,
			graphExpand:    true,
			lexicalChunks:  false,
		})
	case QueryModeHybrid:
		return r.runSearch(ctx, rootPath, query, limit, mode, "hybrid retrieval was explicitly requested", searchOptions{
			exactSymbols:   true,
			lexicalSymbols: true,
			semantic:       true,
			graphExpand:    true,
			lexicalChunks:  true,
		})
	case QueryModeAuto:
		if isIdentifierLike(query) {
			return r.runSearch(ctx, rootPath, query, limit, QueryModeHybrid, "identifier-like query routed to exact symbol, semantic symbol, and chunk retrieval", searchOptions{
				exactSymbols:   true,
				lexicalSymbols: true,
				semantic:       true,
				graphExpand:    true,
				lexicalChunks:  true,
			})
		}
		return r.runSearch(ctx, rootPath, query, limit, QueryModeHybrid, "natural-language query routed to hybrid semantic and symbol retrieval", searchOptions{
			exactSymbols:   false,
			lexicalSymbols: true,
			semantic:       true,
			graphExpand:    true,
			lexicalChunks:  true,
		})
	default:
		return SearchResult{}, fmt.Errorf("unsupported query mode %q", mode)
	}
}

func (r *Router) Definition(ctx context.Context, rootPath string, symbol string) (DefinitionResult, error) {
	def, err := r.store.FindDefinition(ctx, rootPath, symbol)
	if err != nil {
		return DefinitionResult{}, err
	}
	result := DefinitionResult{
		Plan:       Plan{Mode: "exact_symbol_lookup", Reason: "definition queries route directly to symbol lookup"},
		Definition: def,
	}
	if def != nil {
		chunk, chunkErr := r.store.DefinitionChunk(ctx, rootPath, def.SymbolID)
		if chunkErr == nil {
			result.Chunk = chunk
		}
	}
	return result, nil
}

func (r *Router) References(ctx context.Context, rootPath string, symbol string, limit int) (ReferencesResult, error) {
	def, err := r.store.FindDefinition(ctx, rootPath, symbol)
	if err != nil {
		return ReferencesResult{}, err
	}
	if def == nil {
		return ReferencesResult{
			Plan: Plan{Mode: "graph_reference_lookup", Reason: "references queries route to the occurrence graph"},
		}, nil
	}
	refs, err := r.store.ListReferences(ctx, rootPath, symbol, limit)
	if err != nil {
		return ReferencesResult{}, err
	}
	return ReferencesResult{
		Plan:       Plan{Mode: "graph_reference_lookup", Reason: "references queries route to the occurrence graph"},
		Definition: def,
		References: refs,
	}, nil
}

func (r *Router) Context(ctx context.Context, rootPath string, query string, limit int, mode QueryMode) (ContextResult, error) {
	searchResult, err := r.Search(ctx, rootPath, query, max(limit, 1), mode)
	if err != nil {
		return ContextResult{}, err
	}
	if len(searchResult.Hits) == 0 {
		return ContextResult{Plan: searchResult.Plan}, nil
	}

	seed := searchResult.Hits[0]
	result := ContextResult{
		Plan: searchResult.Plan,
		Seed: &seed,
	}

	var wg sync.WaitGroup
	var def *store.DefinitionResult
	var refs []store.ReferenceResult
	var neighbors []store.SearchHit
	var related []store.RelatedChunk

	wg.Add(1)
	go func() {
		defer wg.Done()
		neighbors, _ = r.store.NeighborChunks(ctx, rootPath, seed.FileID, seed.ChunkID, min(4, limit))
	}()

	symbolQuery := query
	if seed.Name != "" {
		symbolQuery = seed.Name
	}
	if seed.PrimarySymbolID > 0 || isIdentifierLike(symbolQuery) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if seed.PrimarySymbolID > 0 {
				related, _ = r.store.RelatedChunks(ctx, rootPath, seed.PrimarySymbolID, min(6, limit))
			}
			if isIdentifierLike(symbolQuery) {
				def, _ = r.store.FindDefinition(ctx, rootPath, symbolQuery)
				if def != nil {
					refs, _ = r.store.ListReferences(ctx, rootPath, def.DisplayName, limit)
					if len(related) == 0 {
						related, _ = r.store.RelatedChunks(ctx, rootPath, def.SymbolID, min(6, limit))
					}
				}
			}
		}()
	}

	wg.Wait()
	result.Definition = def
	result.References = refs
	result.Neighbors = neighbors
	result.GraphNeighbors = related
	return result, nil
}

type searchOptions struct {
	exactSymbols   bool
	lexicalSymbols bool
	semantic       bool
	graphExpand    bool
	lexicalChunks  bool
}

func (r *Router) runSearch(ctx context.Context, rootPath string, query string, limit int, mode QueryMode, reason string, opts searchOptions) (SearchResult, error) {
	signals, err := r.collectSignals(ctx, rootPath, query, limit, opts)
	if err != nil {
		return SearchResult{}, err
	}
	hits, err := r.rankSearchHits(ctx, rootPath, query, limit, signals, opts)
	if err != nil {
		return SearchResult{}, err
	}
	return SearchResult{
		Plan: Plan{Mode: string(mode), Reason: reason},
		Hits: hits,
	}, nil
}

func (r *Router) collectSignals(ctx context.Context, rootPath string, query string, limit int, opts searchOptions) (querySignals, error) {
	var signals querySignals
	var wg sync.WaitGroup
	var exactErr error
	var lexicalSymbolErr error
	var lexicalChunkErr error
	var semanticSymbolErr error
	var semanticChunkErr error

	if opts.exactSymbols {
		wg.Add(1)
		go func() {
			defer wg.Done()
			signals.exactDef, exactErr = r.store.FindDefinition(ctx, rootPath, query)
		}()
	}
	if opts.lexicalSymbols {
		wg.Add(1)
		go func() {
			defer wg.Done()
			signals.lexicalSymbols, lexicalSymbolErr = r.store.SearchSymbols(ctx, rootPath, query, limit)
		}()
	}
	if opts.lexicalChunks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			signals.lexicalChunks, lexicalChunkErr = r.store.SearchChunks(ctx, rootPath, query, limit)
		}()
	}
	if opts.semantic && !isNoopEmbedder(r.embedder) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vectors, err := r.embedder.Embed(ctx, []embed.Document{{
				OwnerType: "query",
				OwnerKey:  query,
				Text:      query,
			}})
			if err != nil {
				semanticChunkErr = err
				return
			}
			if len(vectors) == 0 || len(vectors[0].Values) == 0 {
				return
			}

			var semanticWG sync.WaitGroup
			semanticWG.Add(2)
			go func() {
				defer semanticWG.Done()
				signals.semanticChunks, semanticChunkErr = r.store.SemanticSearchChunks(ctx, rootPath, vectors[0].Values, limit)
			}()
			go func() {
				defer semanticWG.Done()
				signals.semanticSymbols, semanticSymbolErr = r.store.SemanticSearchSymbols(ctx, rootPath, vectors[0].Values, limit)
			}()
			semanticWG.Wait()
		}()
	}

	wg.Wait()
	if exactErr != nil {
		return querySignals{}, exactErr
	}
	if lexicalSymbolErr != nil {
		return querySignals{}, lexicalSymbolErr
	}
	if lexicalChunkErr != nil {
		return querySignals{}, lexicalChunkErr
	}
	if semanticChunkErr != nil {
		return querySignals{}, semanticChunkErr
	}
	if semanticSymbolErr != nil {
		return querySignals{}, semanticSymbolErr
	}
	return signals, nil
}

func (r *Router) rankSearchHits(ctx context.Context, rootPath string, query string, limit int, signals querySignals, opts searchOptions) ([]store.SearchHit, error) {
	if limit <= 0 {
		return nil, nil
	}

	candidates := map[int64]*searchCandidate{}
	seedSymbols := map[int64]float64{}

	addChunk := func(hit store.SearchHit, boost float64) {
		if hit.ChunkID == 0 {
			return
		}
		item, ok := candidates[hit.ChunkID]
		if !ok {
			copyHit := hit
			copyHit.Score = 0
			item = &searchCandidate{hit: copyHit}
			candidates[hit.ChunkID] = item
		}
		item.score += boost
		if boost > 0 && hit.PrimarySymbolID > 0 {
			seedSymbols[hit.PrimarySymbolID] = maxFloat(seedSymbols[hit.PrimarySymbolID], boost)
		}
	}

	addDefinitionChunk := func(symbolID int64, boost float64) {
		if symbolID == 0 {
			return
		}
		chunk, err := r.store.DefinitionChunk(ctx, rootPath, symbolID)
		if err != nil || chunk == nil {
			return
		}
		addChunk(*chunk, boost)
		seedSymbols[symbolID] = maxFloat(seedSymbols[symbolID], boost)
	}

	if signals.exactDef != nil {
		addDefinitionChunk(signals.exactDef.SymbolID, 120)
	}

	for i, hit := range signals.lexicalSymbols {
		boost := 70 * hit.Score * rankWeight(i)
		addDefinitionChunk(hit.SymbolID, boost)
	}
	for i, hit := range signals.semanticSymbols {
		boost := 80 * similarityFromDistance(hit.Score) * rankWeight(i)
		addDefinitionChunk(hit.SymbolID, boost)
	}
	for i, hit := range signals.lexicalChunks {
		addChunk(hit, 28*rankWeight(i))
	}
	for i, hit := range signals.semanticChunks {
		addChunk(hit, 55*similarityFromDistance(hit.Score)*rankWeight(i))
	}

	if opts.graphExpand {
		for _, seed := range topSeedSymbols(seedSymbols, 3) {
			related, err := r.store.RelatedChunks(ctx, rootPath, seed.SymbolID, min(6, limit))
			if err != nil {
				continue
			}
			for _, rel := range related {
				boost := 18 * normalizedSeedWeight(seed.Weight) * graphWeight(rel.RelationKind, rel.Direction)
				addChunk(store.SearchHit{
					ChunkID:         rel.ChunkID,
					FileID:          rel.FileID,
					PrimarySymbolID: rel.SymbolID,
					Path:            rel.Path,
					StartLine:       rel.StartLine,
					EndLine:         rel.EndLine,
					Kind:            rel.Kind,
					Name:            rel.Name,
					HeaderText:      rel.HeaderText,
					Text:            rel.Text,
				}, boost)
			}
		}
	}

	ranked := make([]store.SearchHit, 0, len(candidates))
	for _, item := range candidates {
		item.hit.Score = item.score
		ranked = append(ranked, item.hit)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			if ranked[i].Path == ranked[j].Path {
				return ranked[i].StartLine < ranked[j].StartLine
			}
			return ranked[i].Path < ranked[j].Path
		}
		return ranked[i].Score > ranked[j].Score
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked, nil
}

func normalizeMode(mode QueryMode) (QueryMode, error) {
	if mode == "" {
		return QueryModeAuto, nil
	}
	switch QueryMode(strings.ToLower(string(mode))) {
	case QueryModeAuto, QueryModeHybrid, QueryModeSymbol, QueryModeSemantic, QueryModeGraph:
		return QueryMode(strings.ToLower(string(mode))), nil
	default:
		return "", fmt.Errorf("invalid mode %q", mode)
	}
}

func isIdentifierLike(query string) bool {
	return identifierPattern.MatchString(query)
}

func rankWeight(rank int) float64 {
	return 1.0 / float64(rank+1)
}

func similarityFromDistance(distance float64) float64 {
	if distance <= 0 {
		return 1
	}
	return 1.0 / (1.0 + distance)
}

func graphWeight(kind string, direction string) float64 {
	weight := 1.0
	switch kind {
	case "calls":
		weight = 1.0
	case "imports":
		weight = 0.55
	case "contains":
		weight = 0.4
	default:
		weight = 0.5
	}
	if direction == "incoming" {
		return weight
	}
	return weight * 0.9
}

type weightedSymbol struct {
	SymbolID int64
	Weight   float64
}

func topSeedSymbols(seeds map[int64]float64, limit int) []weightedSymbol {
	items := make([]weightedSymbol, 0, len(seeds))
	for symbolID, weight := range seeds {
		if symbolID == 0 || weight <= 0 {
			continue
		}
		items = append(items, weightedSymbol{SymbolID: symbolID, Weight: weight})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Weight == items[j].Weight {
			return items[i].SymbolID < items[j].SymbolID
		}
		return items[i].Weight > items[j].Weight
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func normalizedSeedWeight(weight float64) float64 {
	if weight <= 0 {
		return 0
	}
	return weight / 120.0
}

func isNoopEmbedder(provider embed.Provider) bool {
	_, ok := provider.(embed.NoopProvider)
	return ok
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

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
