package typescript

import (
	"context"
	"fmt"
	"sort"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/1001encore/wave/internal/indexer"
	"github.com/1001encore/wave/internal/store"
)

func deriveEdges(ctx context.Context, req indexer.DeriveRequest) ([]store.EdgeData, error) {
	moduleSymbols := map[string]string{}
	fileOccurrences := map[string][]store.OccurrenceData{}
	for _, occ := range req.Occurrences {
		fileOccurrences[occ.FilePath] = append(fileOccurrences[occ.FilePath], occ)
	}

	for _, chunk := range req.Chunks {
		if chunk.Kind == "module" && chunk.PrimarySymbol != "" {
			moduleSymbols[chunk.FilePath] = chunk.PrimarySymbol
		}
	}

	var edges []store.EdgeData
	for filePath, source := range req.FileSources {
		moduleSymbol := moduleSymbols[filePath]
		for _, occ := range fileOccurrences[filePath] {
			if occ.IsImport && moduleSymbol != "" && occ.Symbol != "" {
				edges = append(edges, store.EdgeData{
					SrcSymbol:  moduleSymbol,
					DstSymbol:  occ.Symbol,
					Kind:       "imports",
					Provenance: "hybrid",
				})
			}

			enclosingSymbol := enclosingSymbolForOccurrence(filePath, occ, req.Chunks)
			if enclosingSymbol == "" || enclosingSymbol == occ.Symbol || occ.Symbol == "" {
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

		derived, err := deriveCallEdges(ctx, filePath, source, req.Chunks, fileOccurrences[filePath], req.Symbols)
		if err != nil {
			return nil, err
		}
		edges = append(edges, derived...)

		returnEdges, err := deriveReturnEdges(ctx, filePath, source, req.Chunks, fileOccurrences[filePath])
		if err != nil {
			return nil, err
		}
		edges = append(edges, returnEdges...)
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
	return edges, nil
}

func deriveCallEdges(
	ctx context.Context,
	filePath string,
	source []byte,
	chunks []store.ChunkData,
	occurrences []store.OccurrenceData,
	symbols map[string]store.SymbolData,
) ([]store.EdgeData, error) {
	parser := tree_sitter.NewParser()
	defer parser.Close()

	lang := tree_sitter.NewLanguage(languageForFile(filePath))
	if err := parser.SetLanguage(lang); err != nil {
		return nil, fmt.Errorf("set typescript grammar: %w", err)
	}
	tree := parser.ParseCtx(ctx, source, nil)
	if tree == nil {
		return nil, fmt.Errorf("parse %s for edge derivation: nil syntax tree", filePath)
	}
	defer tree.Close()

	root := tree.RootNode()
	if root == nil {
		return nil, fmt.Errorf("parse %s for edge derivation: nil root node", filePath)
	}

	var edges []store.EdgeData
	var visit func(*tree_sitter.Node)
	visit = func(node *tree_sitter.Node) {
		if node == nil {
			return
		}
		if node.Kind() == "call_expression" || node.Kind() == "new_expression" {
			src := enclosingSymbolForNode(filePath, node, chunks)
			dst := calleeSymbolForNode(node, occurrences)
			if src != "" && dst != "" {
				kind := "calls"
				if node.Kind() == "new_expression" {
					kind = "instantiates"
				} else if symbol, ok := symbols[dst]; ok && symbol.Kind == "class" {
					kind = "instantiates"
				}
				edges = append(edges, store.EdgeData{
					SrcSymbol:  src,
					DstSymbol:  dst,
					Kind:       kind,
					Provenance: "hybrid",
				})
			}
		}

		cursor := node.Walk()
		defer cursor.Close()
		for _, child := range node.NamedChildren(cursor) {
			childCopy := child
			visit(&childCopy)
		}
	}
	visit(root)
	return edges, nil
}

func deriveReturnEdges(
	ctx context.Context,
	filePath string,
	source []byte,
	chunks []store.ChunkData,
	occurrences []store.OccurrenceData,
) ([]store.EdgeData, error) {
	parser := tree_sitter.NewParser()
	defer parser.Close()

	lang := tree_sitter.NewLanguage(languageForFile(filePath))
	if err := parser.SetLanguage(lang); err != nil {
		return nil, fmt.Errorf("set typescript grammar: %w", err)
	}
	tree := parser.ParseCtx(ctx, source, nil)
	if tree == nil {
		return nil, fmt.Errorf("parse %s for edge derivation: nil syntax tree", filePath)
	}
	defer tree.Close()

	root := tree.RootNode()
	if root == nil {
		return nil, fmt.Errorf("parse %s for edge derivation: nil root node", filePath)
	}

	var edges []store.EdgeData
	var visit func(*tree_sitter.Node)
	visit = func(node *tree_sitter.Node) {
		if node == nil {
			return
		}
		if node.Kind() == "return_statement" {
			src := enclosingSymbolForNode(filePath, node, chunks)
			if src != "" {
				for _, occ := range symbolsWithinNode(node, occurrences) {
					edges = append(edges, store.EdgeData{
						SrcSymbol:  src,
						DstSymbol:  occ.Symbol,
						Kind:       "returns",
						Provenance: "hybrid",
					})
				}
			}
		}

		cursor := node.Walk()
		defer cursor.Close()
		for _, child := range node.NamedChildren(cursor) {
			childCopy := child
			visit(&childCopy)
		}
	}
	visit(root)
	return edges, nil
}

func symbolsWithinNode(node *tree_sitter.Node, occurrences []store.OccurrenceData) []store.OccurrenceData {
	start := node.StartPosition()
	end := node.EndPosition()
	seen := map[string]struct{}{}
	out := make([]store.OccurrenceData, 0, 2)
	for _, occ := range occurrences {
		if occ.Symbol == "" || occ.IsDefinition || occ.IsImport || !occ.IsRead {
			continue
		}
		if !rangeWithin(occ.StartLine, occ.StartCol, occ.EndLine, occ.EndCol, int(start.Row), int(start.Column), int(end.Row), int(end.Column)) {
			continue
		}
		if _, ok := seen[occ.Symbol]; ok {
			continue
		}
		seen[occ.Symbol] = struct{}{}
		out = append(out, occ)
	}
	return out
}

func enclosingSymbolForOccurrence(filePath string, occ store.OccurrenceData, chunks []store.ChunkData) string {
	bestWidth := 0
	bestSymbol := ""
	for _, chunk := range chunks {
		if chunk.FilePath != filePath || chunk.PrimarySymbol == "" {
			continue
		}
		if chunk.Kind == "import_statement" {
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

func enclosingSymbolForNode(filePath string, node *tree_sitter.Node, chunks []store.ChunkData) string {
	start := node.StartPosition()
	end := node.EndPosition()
	bestWidth := 0
	bestSymbol := ""
	for _, chunk := range chunks {
		if chunk.FilePath != filePath || chunk.PrimarySymbol == "" {
			continue
		}
		switch chunk.Kind {
		case "import_statement", "interface_declaration", "type_alias_declaration", "enum_declaration", "public_field_definition", "method_signature", "abstract_method_signature":
			continue
		}
		if chunk.StartLine > int(start.Row) || chunk.EndLine < int(end.Row) {
			continue
		}
		width := chunk.EndLine - chunk.StartLine
		if bestSymbol == "" || width < bestWidth {
			bestSymbol = chunk.PrimarySymbol
			bestWidth = width
		}
	}
	return bestSymbol
}

func calleeSymbolForNode(node *tree_sitter.Node, occurrences []store.OccurrenceData) string {
	functionNode := node.ChildByFieldName("function")
	if functionNode == nil {
		functionNode = node.ChildByFieldName("constructor")
	}
	if functionNode == nil {
		functionNode = node
	}
	start := functionNode.StartPosition()
	end := functionNode.EndPosition()

	bestSymbol := ""
	bestSpan := 0
	for _, occ := range occurrences {
		if occ.Symbol == "" || occ.IsDefinition || occ.IsImport {
			continue
		}
		if !rangeWithin(occ.StartLine, occ.StartCol, occ.EndLine, occ.EndCol, int(start.Row), int(start.Column), int(end.Row), int(end.Column)) {
			continue
		}
		span := occ.EndLine*100000 + occ.EndCol - (occ.StartLine*100000 + occ.StartCol)
		if bestSymbol == "" || span < bestSpan {
			bestSymbol = occ.Symbol
			bestSpan = span
		}
	}
	return bestSymbol
}

func rangeWithin(
	startLine int,
	startCol int,
	endLine int,
	endCol int,
	outerStartLine int,
	outerStartCol int,
	outerEndLine int,
	outerEndCol int,
) bool {
	if startLine < outerStartLine || endLine > outerEndLine {
		return false
	}
	if startLine == outerStartLine && startCol < outerStartCol {
		return false
	}
	if endLine == outerEndLine && endCol > outerEndCol {
		return false
	}
	return true
}
