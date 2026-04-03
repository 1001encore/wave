package golang

import (
	"context"
	"testing"

	"github.com/1001encore/wave/internal/syntax"
)

func TestSyntaxExtractorExtractsGoChunks(t *testing.T) {
	source := []byte(`package main

import "fmt"

type Service struct{}

func (s Service) Run() {}

func main() {
	fmt.Println("ok")
}
`)

	chunks, err := SyntaxExtractor{}.Extract(context.Background(), "main.go", source)
	if err != nil {
		t.Fatalf("extract chunks: %v", err)
	}

	assertChunk(t, chunks, "module", "main.go")
	assertChunk(t, chunks, "import_declaration", `import "fmt"`)
	assertChunk(t, chunks, "type_declaration", `type Service struct{}`)
	assertChunk(t, chunks, "function_declaration", "main")
}

func assertChunk(t *testing.T, chunks []syntax.Chunk, kind string, name string) {
	t.Helper()
	for _, chunk := range chunks {
		if chunk.Kind == kind && chunk.Name == name {
			return
		}
	}
	t.Fatalf("missing chunk kind=%q name=%q", kind, name)
}
