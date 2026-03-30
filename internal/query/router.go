package query

import (
	"context"
	"regexp"
	"sort"
	"sync"

	"wave/internal/embed"
	"wave/internal/store"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_./:#()\-]*$`)

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

func NewRouter(st *store.Store, embedder embed.Provider) *Router {
	return &Router{
		store:    st,
		embedder: embedder,
	}
}

func (r *Router) Search(ctx context.Context, rootPath string, query string, limit int) (SearchResult, error) {
	if isIdentifierLike(query) {
		var wg sync.WaitGroup
		var def *store.DefinitionResult
		var hits []store.SearchHit
		var defErr error
		var searchErr error

		wg.Add(2)
		go func() {
			defer wg.Done()
			def, defErr = r.store.FindDefinition(ctx, rootPath, query)
		}()
		go func() {
			defer wg.Done()
			hits, searchErr = r.store.SearchChunks(ctx, rootPath, query, limit)
		}()
		wg.Wait()
		if defErr != nil {
			return SearchResult{}, defErr
		}
		if searchErr != nil {
			return SearchResult{}, searchErr
		}

		if def != nil {
			if chunk, err := r.store.DefinitionChunk(ctx, rootPath, def.SymbolID); err == nil && chunk != nil {
				hits = promoteChunk(*chunk, hits, limit)
			}
			return SearchResult{
				Plan: Plan{Mode: "hybrid_symbol_search", Reason: "identifier-like query searched exact symbols and chunks"},
				Hits: hits,
			}, nil
		}

		return SearchResult{
			Plan: Plan{Mode: "lexical_chunk_search", Reason: "identifier-like query had no exact symbol match; falling back to chunk search"},
			Hits: hits,
		}, nil
	}

	lexicalHits, semanticHits, err := r.semanticChunkSearch(ctx, rootPath, query, limit)
	if err != nil {
		return SearchResult{}, err
	}
	hits := mergeSearchHits(semanticHits, lexicalHits, limit)
	mode := "lexical_chunk_search"
	reason := "natural-language query routed to chunk retrieval text search"
	if len(semanticHits) > 0 {
		mode = "hybrid_semantic_search"
		reason = "natural-language query used embeddings and lexical chunk retrieval"
	}
	return SearchResult{
		Plan: Plan{Mode: mode, Reason: reason},
		Hits: hits,
	}, nil
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

func (r *Router) Context(ctx context.Context, rootPath string, query string, limit int) (ContextResult, error) {
	searchResult, err := r.Search(ctx, rootPath, query, max(limit, 1))
	if err != nil {
		return ContextResult{}, err
	}
	if len(searchResult.Hits) == 0 {
		return ContextResult{
			Plan: searchResult.Plan,
		}, nil
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
	if isIdentifierLike(symbolQuery) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			def, _ = r.store.FindDefinition(ctx, rootPath, symbolQuery)
			if def != nil {
				refs, _ = r.store.ListReferences(ctx, rootPath, def.DisplayName, limit)
				related, _ = r.store.RelatedChunks(ctx, rootPath, def.SymbolID, min(6, limit))
			} else if seed.PrimarySymbolID > 0 {
				related, _ = r.store.RelatedChunks(ctx, rootPath, seed.PrimarySymbolID, min(6, limit))
			}
		}()
	} else if seed.PrimarySymbolID > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			related, _ = r.store.RelatedChunks(ctx, rootPath, seed.PrimarySymbolID, min(6, limit))
		}()
	}

	wg.Wait()
	result.Definition = def
	result.References = refs
	result.Neighbors = neighbors
	result.GraphNeighbors = related
	return result, nil
}

func isIdentifierLike(query string) bool {
	return identifierPattern.MatchString(query)
}

func promoteChunk(chunk store.SearchHit, hits []store.SearchHit, limit int) []store.SearchHit {
	result := make([]store.SearchHit, 0, len(hits)+1)
	result = append(result, chunk)
	seen := map[int64]struct{}{chunk.ChunkID: {}}
	for _, hit := range hits {
		if _, ok := seen[hit.ChunkID]; ok {
			continue
		}
		seen[hit.ChunkID] = struct{}{}
		result = append(result, hit)
		if len(result) >= limit {
			break
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].ChunkID == chunk.ChunkID {
			return true
		}
		if result[j].ChunkID == chunk.ChunkID {
			return false
		}
		return result[i].Score < result[j].Score
	})
	if len(result) > limit {
		return result[:limit]
	}
	return result
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

func (r *Router) semanticChunkSearch(ctx context.Context, rootPath string, query string, limit int) ([]store.SearchHit, []store.SearchHit, error) {
	var wg sync.WaitGroup
	var lexicalHits []store.SearchHit
	var semanticHits []store.SearchHit
	var lexicalErr error
	var semanticErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		lexicalHits, lexicalErr = r.store.SearchChunks(ctx, rootPath, query, limit)
	}()

	if !isNoopEmbedder(r.embedder) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vectors, err := r.embedder.Embed(ctx, []embed.Document{{
				OwnerType: "query",
				OwnerKey:  query,
				Text:      query,
			}})
			if err != nil {
				semanticErr = err
				return
			}
			if len(vectors) == 0 || len(vectors[0].Values) == 0 {
				return
			}
			semanticHits, semanticErr = r.store.SemanticSearchChunks(ctx, rootPath, vectors[0].Values, limit)
		}()
	}

	wg.Wait()
	if lexicalErr != nil {
		return nil, nil, lexicalErr
	}
	if semanticErr != nil {
		return nil, nil, semanticErr
	}
	return lexicalHits, semanticHits, nil
}

func mergeSearchHits(primary []store.SearchHit, secondary []store.SearchHit, limit int) []store.SearchHit {
	if limit <= 0 {
		return nil
	}
	result := make([]store.SearchHit, 0, min(limit, len(primary)+len(secondary)))
	seen := make(map[int64]struct{}, limit)
	appendHits := func(hits []store.SearchHit) {
		for _, hit := range hits {
			if len(result) >= limit {
				return
			}
			if _, ok := seen[hit.ChunkID]; ok {
				continue
			}
			seen[hit.ChunkID] = struct{}{}
			result = append(result, hit)
		}
	}
	appendHits(primary)
	appendHits(secondary)
	return result
}

func isNoopEmbedder(provider embed.Provider) bool {
	_, ok := provider.(embed.NoopProvider)
	return ok
}
