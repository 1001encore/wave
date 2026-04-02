package query

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/1001encore/wave/internal/embed"
	"github.com/1001encore/wave/internal/store"
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
	Plan       Plan                     `json:"plan"`
	Definition *store.DefinitionResult  `json:"definition,omitempty"`
	Chunk      *store.SearchHit         `json:"chunk,omitempty"`
	Candidates []store.DefinitionResult `json:"candidates,omitempty"`
}

type ReferencesResult struct {
	Plan       Plan                     `json:"plan"`
	Definition *store.DefinitionResult  `json:"definition,omitempty"`
	References []store.ReferenceResult  `json:"references,omitempty"`
	Candidates []store.DefinitionResult `json:"candidates,omitempty"`
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
	exactDef       *store.DefinitionResult
	lexicalSymbols []store.SymbolSearchHit
	lexicalChunks  []store.SearchHit
	semanticChunks []store.SearchHit
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
	candidates, err := r.store.FindDefinitions(ctx, rootPath, symbol, 8)
	if err != nil {
		return DefinitionResult{}, err
	}
	def, ambiguous := resolveDefinition(symbol, candidates)
	defs := exactDefinitionCandidates(symbol, candidates)
	if len(defs) == 0 && def != nil {
		defs = []store.DefinitionResult{*def}
	}
	result := DefinitionResult{
		Plan:       Plan{Mode: "exact_symbol_lookup", Reason: "definition queries route directly to symbol lookup"},
		Definition: def,
	}
	if len(defs) > 1 || ambiguous {
		result.Candidates = candidates
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
	candidates, err := r.store.FindDefinitions(ctx, rootPath, symbol, 8)
	if err != nil {
		return ReferencesResult{}, err
	}
	def, ambiguous := resolveDefinition(symbol, candidates)
	defs := exactDefinitionCandidates(symbol, candidates)
	if len(defs) == 0 && def != nil {
		defs = []store.DefinitionResult{*def}
	}
	result := ReferencesResult{
		Plan:       Plan{Mode: "graph_reference_lookup", Reason: "references queries route to the occurrence graph"},
		Definition: def,
	}
	if len(defs) > 1 || ambiguous {
		result.Candidates = candidates
	}
	if len(defs) == 0 {
		return result, nil
	}
	refs, err := r.store.ListReferencesBySymbolIDs(ctx, rootPath, definitionSymbolIDs(defs), limit)
	if err != nil {
		return ReferencesResult{}, err
	}
	result.References = refs
	return result, nil
}

func (r *Router) Context(ctx context.Context, rootPath string, query string, limit int, mode QueryMode) (ContextResult, error) {
	searchResult, err := r.Search(ctx, rootPath, query, max(limit, 1), mode)
	if err != nil {
		return ContextResult{}, err
	}
	if len(searchResult.Hits) == 0 {
		return ContextResult{Plan: searchResult.Plan}, nil
	}

	seed := chooseContextSeed(searchResult.Hits, query)
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
	if seed.ChunkID > 0 || isIdentifierLike(symbolQuery) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if seed.ChunkID > 0 {
				related = r.relatedChunksForSeedChunk(ctx, rootPath, seed.ChunkID, limit)
			}
			if isIdentifierLike(symbolQuery) {
				candidates, _ := r.store.FindDefinitions(ctx, rootPath, symbolQuery, 8)
				def, _ = resolveDefinition(symbolQuery, candidates)
				if def != nil {
					refs, _ = r.store.ListReferencesBySymbolID(ctx, rootPath, def.SymbolID, limit)
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
	var semanticChunkErr error

	if opts.exactSymbols {
		wg.Add(1)
		go func() {
			defer wg.Done()
			candidates, err := r.store.FindDefinitions(ctx, rootPath, query, 8)
			if err != nil {
				exactErr = err
				return
			}
			signals.exactDef, _ = resolveDefinition(query, candidates)
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
			semanticWG.Add(1)
			go func() {
				defer semanticWG.Done()
				signals.semanticChunks, semanticChunkErr = r.store.SemanticSearchChunks(ctx, rootPath, vectors[0].Values, limit)
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
	for i, hit := range signals.lexicalChunks {
		addChunk(hit, 28*rankWeight(i))
	}
	for i, hit := range signals.semanticChunks {
		addChunk(hit, 55*similarityFromDistance(hit.Score)*rankWeight(i))
	}

	for _, link := range r.chunkSeedLinks(ctx, rootPath, candidates, 6) {
		boost := link.Weight
		if boost <= 0 {
			continue
		}
		seedSymbols[link.SymbolID] = maxFloat(seedSymbols[link.SymbolID], boost)
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
		item.hit.Score = rerankChunkScore(item.hit, item.score)
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

func (r *Router) chunkSeedLinks(ctx context.Context, rootPath string, candidates map[int64]*searchCandidate, limit int) []store.ChunkSymbolLink {
	if len(candidates) == 0 || limit <= 0 {
		return nil
	}

	type scoredChunk struct {
		ChunkID int64
		Score   float64
	}

	chunks := make([]scoredChunk, 0, len(candidates))
	for chunkID, candidate := range candidates {
		chunks = append(chunks, scoredChunk{
			ChunkID: chunkID,
			Score:   candidate.score,
		})
	}
	sort.SliceStable(chunks, func(i, j int) bool {
		if chunks[i].Score == chunks[j].Score {
			return chunks[i].ChunkID < chunks[j].ChunkID
		}
		return chunks[i].Score > chunks[j].Score
	})
	if len(chunks) > limit {
		chunks = chunks[:limit]
	}

	chunkIDs := make([]int64, 0, len(chunks))
	chunkScores := make(map[int64]float64, len(chunks))
	for _, chunk := range chunks {
		chunkIDs = append(chunkIDs, chunk.ChunkID)
		chunkScores[chunk.ChunkID] = chunk.Score
	}

	links, err := r.store.LinkedSymbolsForChunks(ctx, rootPath, chunkIDs)
	if err != nil {
		return nil
	}
	for i := range links {
		links[i].Weight *= normalizedSeedWeight(chunkScores[links[i].ChunkID]) * roleWeight(links[i].Role)
	}
	return links
}

func (r *Router) relatedChunksForSeedChunk(ctx context.Context, rootPath string, chunkID int64, limit int) []store.RelatedChunk {
	if chunkID == 0 || limit <= 0 {
		return nil
	}
	links, err := r.store.LinkedSymbolsForChunks(ctx, rootPath, []int64{chunkID})
	if err != nil || len(links) == 0 {
		return nil
	}

	type symbolSeed struct {
		SymbolID int64
		Weight   float64
	}

	weights := map[int64]float64{}
	for _, link := range links {
		weights[link.SymbolID] = maxFloat(weights[link.SymbolID], link.Weight*roleWeight(link.Role))
	}
	seeds := make([]symbolSeed, 0, len(weights))
	for symbolID, weight := range weights {
		seeds = append(seeds, symbolSeed{SymbolID: symbolID, Weight: weight})
	}
	sort.SliceStable(seeds, func(i, j int) bool {
		if seeds[i].Weight == seeds[j].Weight {
			return seeds[i].SymbolID < seeds[j].SymbolID
		}
		return seeds[i].Weight > seeds[j].Weight
	})
	if len(seeds) > 4 {
		seeds = seeds[:4]
	}

	seen := map[int64]struct{}{}
	related := make([]store.RelatedChunk, 0, limit)
	for _, seed := range seeds {
		chunks, err := r.store.RelatedChunks(ctx, rootPath, seed.SymbolID, min(6, limit))
		if err != nil {
			continue
		}
		for _, chunk := range chunks {
			if _, ok := seen[chunk.ChunkID]; ok {
				continue
			}
			seen[chunk.ChunkID] = struct{}{}
			related = append(related, chunk)
			if len(related) >= limit {
				return related
			}
		}
	}
	return related
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
	case "reference":
		weight = 0.95
	case "implementation":
		weight = 0.9
	case "type_definition":
		weight = 0.8
	case "imports":
		weight = 0.55
	case "writes":
		weight = 0.7
	case "reads":
		weight = 0.65
	case "uses":
		weight = 0.6
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

func roleWeight(role string) float64 {
	switch role {
	case "defines":
		return 1.0
	case "writes":
		return 0.8
	case "reads":
		return 0.7
	case "imports":
		return 0.65
	case "uses":
		return 0.6
	default:
		return 0.5
	}
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

func resolveDefinition(query string, candidates []store.DefinitionResult) (*store.DefinitionResult, bool) {
	if len(candidates) == 0 {
		return nil, false
	}

	var exact []store.DefinitionResult
	for _, candidate := range candidates {
		if candidate.DisplayName == query || candidate.ScipSymbol == query {
			exact = append(exact, candidate)
		}
	}
	if len(exact) == 1 {
		return &exact[0], false
	}
	if len(exact) > 1 {
		best := rankDefinitionCandidates(exact)
		return &best, true
	}
	if len(candidates) == 1 {
		return &candidates[0], false
	}
	best := rankDefinitionCandidates(candidates)
	return &best, true
}

func exactDefinitionCandidates(query string, candidates []store.DefinitionResult) []store.DefinitionResult {
	exact := make([]store.DefinitionResult, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.DisplayName == query || candidate.ScipSymbol == query {
			exact = append(exact, candidate)
		}
	}
	return exact
}

func definitionSymbolIDs(defs []store.DefinitionResult) []int64 {
	if len(defs) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(defs))
	seen := make(map[int64]struct{}, len(defs))
	for _, def := range defs {
		if _, ok := seen[def.SymbolID]; ok {
			continue
		}
		seen[def.SymbolID] = struct{}{}
		ids = append(ids, def.SymbolID)
	}
	return ids
}

func rankDefinitionCandidates(candidates []store.DefinitionResult) store.DefinitionResult {
	best := candidates[0]
	bestScore := definitionSpecificity(best)
	for _, c := range candidates[1:] {
		score := definitionSpecificity(c)
		if score > bestScore {
			best = c
			bestScore = score
		}
	}
	return best
}

func definitionSpecificity(d store.DefinitionResult) float64 {
	score := 0.0
	if d.Path != "" {
		score += 10.0
	}
	switch d.Kind {
	case "function", "method":
		score += 5.0
	case "class":
		score += 4.0
	case "interface":
		score += 3.5
	case "type", "type_alias":
		score += 3.0
	case "variable", "property":
		score += 2.0
	case "module":
		score += 1.0
	}
	if d.DocSummary != "" {
		score += 1.0
	}
	span := (d.EndLine - d.StartLine) + 1
	if span > 0 {
		score += 1.0 / float64(span)
	}
	return score
}

func chooseContextSeed(hits []store.SearchHit, query string) store.SearchHit {
	best := hits[0]
	bestScore := contextSeedScore(best, query)
	for _, hit := range hits[1:] {
		score := contextSeedScore(hit, query)
		if score > bestScore {
			best = hit
			bestScore = score
		}
	}
	return best
}

func contextSeedScore(hit store.SearchHit, query string) float64 {
	score := rerankChunkScore(hit, hit.Score)
	switch hit.Kind {
	case "function_definition", "method_definition", "class_definition", "interface_declaration", "class_declaration":
		score *= 1.3
	case "module":
		score *= 0.22
	case "import_statement", "import_from_statement":
		score *= 0.35
	}
	if isIdentifierLike(query) && hit.Name == query {
		score *= 1.6
	}
	return score
}

func rerankChunkScore(hit store.SearchHit, base float64) float64 {
	score := base
	lineSpan := max(1, hit.EndLine-hit.StartLine+1)
	switch hit.Kind {
	case "module":
		score *= 0.3
	case "import_statement", "import_from_statement":
		score *= 0.45
	case "function_definition", "method_definition", "class_definition", "class_declaration", "interface_declaration":
		score *= 1.15
	}
	if lineSpan > 40 {
		score *= 40.0 / float64(lineSpan)
	}
	return score
}
