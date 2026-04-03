package java

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"

	"github.com/1001encore/wave/internal/syntax"
)

type SyntaxExtractor struct{}

func (SyntaxExtractor) Extract(ctx context.Context, filePath string, source []byte) ([]syntax.Chunk, error) {
	parser := tree_sitter.NewParser()
	defer parser.Close()

	lang := tree_sitter.NewLanguage(tree_sitter_java.Language())
	if err := parser.SetLanguage(lang); err != nil {
		return nil, fmt.Errorf("set java grammar: %w", err)
	}

	tree := parser.ParseCtx(ctx, source, nil)
	if tree == nil {
		return nil, fmt.Errorf("parse %s: nil syntax tree", filePath)
	}
	defer tree.Close()

	root := tree.RootNode()
	if root == nil {
		return nil, fmt.Errorf("parse %s: nil root node", filePath)
	}

	chunks := []syntax.Chunk{newChunk(filePath, "module", filepath.Base(filePath), "", root, source)}
	walkNode(filePath, source, root, &chunks)
	assignParents(chunks)
	return chunks, nil
}

func walkNode(filePath string, source []byte, node *tree_sitter.Node, chunks *[]syntax.Chunk) {
	if node == nil {
		return
	}

	switch node.Kind() {
	case "package_declaration",
		"import_declaration",
		"class_declaration",
		"interface_declaration",
		"enum_declaration",
		"record_declaration",
		"method_declaration",
		"constructor_declaration",
		"field_declaration":
		*chunks = append(*chunks, buildNamedChunk(filePath, node.Kind(), node, source))
	}

	cursor := node.Walk()
	defer cursor.Close()
	for _, child := range node.NamedChildren(cursor) {
		childCopy := child
		walkNode(filePath, source, &childCopy, chunks)
	}
}

func buildNamedChunk(filePath string, kind string, node *tree_sitter.Node, source []byte) syntax.Chunk {
	return newChunk(filePath, kind, extractNodeName(node, source), "", node, source)
}

func newChunk(filePath string, kind string, name string, parentKey string, node *tree_sitter.Node, source []byte) syntax.Chunk {
	startByte, endByte := node.ByteRange()
	start := node.StartPosition()
	end := node.EndPosition()
	text := node.Utf8Text(source)
	header := text
	if idx := strings.IndexByte(header, '\n'); idx >= 0 {
		header = header[:idx]
	}
	header = strings.TrimSpace(header)
	if len(header) > 160 {
		header = header[:160]
	}

	key := fmt.Sprintf("%s:%d:%d:%s", filePath, startByte, endByte, kind)
	return syntax.Chunk{
		Key:        key,
		FilePath:   filePath,
		Kind:       kind,
		Name:       name,
		ParentKey:  parentKey,
		StartByte:  int(startByte),
		EndByte:    int(endByte),
		StartLine:  int(start.Row),
		StartCol:   int(start.Column),
		EndLine:    int(end.Row),
		EndCol:     int(end.Column),
		Text:       text,
		HeaderText: header,
	}
}

func assignParents(chunks []syntax.Chunk) {
	sort.Slice(chunks, func(i, j int) bool {
		if chunks[i].StartByte == chunks[j].StartByte {
			return chunks[i].EndByte > chunks[j].EndByte
		}
		return chunks[i].StartByte < chunks[j].StartByte
	})

	for i := range chunks {
		bestWidth := 0
		for j := range chunks {
			if i == j {
				continue
			}
			if chunks[j].StartByte <= chunks[i].StartByte && chunks[j].EndByte >= chunks[i].EndByte {
				width := chunks[j].EndByte - chunks[j].StartByte
				if width == 0 {
					continue
				}
				if bestWidth == 0 || width < bestWidth {
					bestWidth = width
					chunks[i].ParentKey = chunks[j].Key
				}
			}
		}
	}

	for i := range chunks {
		if chunks[i].ParentKey == chunks[i].Key {
			chunks[i].ParentKey = ""
		}
	}
}

func extractNodeName(node *tree_sitter.Node, source []byte) string {
	if node == nil {
		return ""
	}

	if name := node.ChildByFieldName("name"); name != nil {
		return strings.TrimSpace(name.Utf8Text(source))
	}

	text := strings.TrimSpace(node.Utf8Text(source))
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	if len(text) > 80 {
		text = text[:80]
	}
	return text
}
