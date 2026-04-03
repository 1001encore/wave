package indexer

import (
	"sort"

	"github.com/1001encore/wave/internal/store"
)

// DeriveOccurrenceEdges derives imports/defines/reads/writes/uses edges directly
// from SCIP occurrences and chunk ownership.
func DeriveOccurrenceEdges(req DeriveRequest, ignoredChunkKinds ...string) []store.EdgeData {
	moduleSymbols := map[string]string{}
	fileOccurrences := map[string][]store.OccurrenceData{}
	ignoredKinds := map[string]struct{}{}
	for _, kind := range ignoredChunkKinds {
		ignoredKinds[kind] = struct{}{}
	}

	for _, occ := range req.Occurrences {
		fileOccurrences[occ.FilePath] = append(fileOccurrences[occ.FilePath], occ)
	}

	for _, chunk := range req.Chunks {
		if chunk.Kind == "module" && chunk.PrimarySymbol != "" {
			moduleSymbols[chunk.FilePath] = chunk.PrimarySymbol
		}
	}

	var edges []store.EdgeData
	for filePath, occs := range fileOccurrences {
		moduleSymbol := moduleSymbols[filePath]
		for _, occ := range occs {
			if occ.Symbol == "" {
				continue
			}

			if occ.IsImport && moduleSymbol != "" {
				edges = append(edges, store.EdgeData{
					SrcSymbol:  moduleSymbol,
					DstSymbol:  occ.Symbol,
					Kind:       "imports",
					Provenance: "hybrid",
				})
			}

			enclosingSymbol := enclosingSymbolForOccurrence(filePath, occ, req.Chunks, ignoredKinds)
			if enclosingSymbol == "" || enclosingSymbol == occ.Symbol {
				continue
			}

			if occ.IsDefinition && !occ.IsImport {
				edges = append(edges, store.EdgeData{
					SrcSymbol:  enclosingSymbol,
					DstSymbol:  occ.Symbol,
					Kind:       "defines",
					Provenance: "hybrid",
				})
			}
			if occ.IsWrite {
				edges = append(edges, store.EdgeData{
					SrcSymbol:  enclosingSymbol,
					DstSymbol:  occ.Symbol,
					Kind:       "writes",
					Provenance: "hybrid",
				})
			}
			if occ.IsRead {
				edges = append(edges, store.EdgeData{
					SrcSymbol:  enclosingSymbol,
					DstSymbol:  occ.Symbol,
					Kind:       "reads",
					Provenance: "hybrid",
				})
			}
			if !occ.IsDefinition && !occ.IsImport && !occ.IsRead && !occ.IsWrite {
				edges = append(edges, store.EdgeData{
					SrcSymbol:  enclosingSymbol,
					DstSymbol:  occ.Symbol,
					Kind:       "uses",
					Provenance: "hybrid",
				})
			}
		}
	}

	sort.Slice(edges, func(i, j int) bool {
		if edges[i].SrcSymbol == edges[j].SrcSymbol {
			if edges[i].DstSymbol == edges[j].DstSymbol {
				return edges[i].Kind < edges[j].Kind
			}
			return edges[i].DstSymbol < edges[j].DstSymbol
		}
		return edges[i].SrcSymbol < edges[j].SrcSymbol
	})
	return edges
}

func enclosingSymbolForOccurrence(
	filePath string,
	occ store.OccurrenceData,
	chunks []store.ChunkData,
	ignoredKinds map[string]struct{},
) string {
	bestWidth := 0
	bestSymbol := ""
	for _, chunk := range chunks {
		if chunk.FilePath != filePath || chunk.PrimarySymbol == "" {
			continue
		}
		if _, ignored := ignoredKinds[chunk.Kind]; ignored {
			continue
		}
		if !rangeWithin(
			occ.EnclosingStartLine,
			occ.EnclosingStartCol,
			occ.EnclosingEndLine,
			occ.EnclosingEndCol,
			chunk.StartLine,
			chunk.StartCol,
			chunk.EndLine,
			chunk.EndCol,
		) {
			continue
		}

		width := (chunk.EndLine-chunk.StartLine)*100000 + (chunk.EndCol - chunk.StartCol)
		if bestSymbol == "" || width < bestWidth {
			bestSymbol = chunk.PrimarySymbol
			bestWidth = width
		}
	}
	return bestSymbol
}

func rangeWithin(
	startLine int,
	startCol int,
	endLine int,
	endCol int,
	containerStartLine int,
	containerStartCol int,
	containerEndLine int,
	containerEndCol int,
) bool {
	if startLine < containerStartLine || endLine > containerEndLine {
		return false
	}
	if startLine == containerStartLine && startCol < containerStartCol {
		return false
	}
	if endLine == containerEndLine && endCol > containerEndCol {
		return false
	}
	return true
}
