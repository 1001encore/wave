package typescript

import (
	"context"
	"testing"

	"github.com/1001encore/wave/internal/syntax"
)

func TestSyntaxExtractorExtractsTypeScriptChunks(t *testing.T) {
	source := []byte(`import { helper } from "./lib/util"

export interface Greeter {
  greet(name: string): string
}

export class ConsoleGreeter {
  greet(name: string): string {
    return helper(name)
  }
}

export const makeGreeter = (prefix: string) => {
  return (name: string) => helper(prefix + name)
}
`)

	chunks, err := SyntaxExtractor{}.Extract(context.Background(), "src/index.ts", source)
	if err != nil {
		t.Fatalf("extract chunks: %v", err)
	}

	assertChunk(t, chunks, "module", "index.ts")
	assertChunk(t, chunks, "import_statement", `import { helper } from "./lib/util"`)
	assertChunk(t, chunks, "interface_declaration", "Greeter")
	assertChunk(t, chunks, "class_declaration", "ConsoleGreeter")
	assertChunk(t, chunks, "method_definition", "greet")
	assertChunk(t, chunks, "lexical_declaration", "makeGreeter")
}

func TestSyntaxExtractorUsesTSXGrammar(t *testing.T) {
	source := []byte(`export const Button = () => <button>Hello</button>`)

	chunks, err := SyntaxExtractor{}.Extract(context.Background(), "src/component.tsx", source)
	if err != nil {
		t.Fatalf("extract tsx chunks: %v", err)
	}

	assertChunk(t, chunks, "module", "component.tsx")
	assertChunk(t, chunks, "lexical_declaration", "Button")
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
