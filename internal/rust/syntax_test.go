package rust

import (
	"context"
	"testing"

	"github.com/1001encore/wave/internal/syntax"
)

func TestSyntaxExtractorExtractsRustChunks(t *testing.T) {
	source := []byte(`use std::fmt::Debug;

pub struct Worker;

impl Worker {
    pub fn run(&self) {}
}

pub fn execute() {}
`)

	chunks, err := SyntaxExtractor{}.Extract(context.Background(), "lib.rs", source)
	if err != nil {
		t.Fatalf("extract chunks: %v", err)
	}

	assertChunk(t, chunks, "module", "lib.rs")
	assertChunk(t, chunks, "use_declaration", "use std::fmt::Debug;")
	assertChunk(t, chunks, "struct_item", "Worker")
	assertChunk(t, chunks, "impl_item", "Worker")
	assertChunk(t, chunks, "function_item", "execute")
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
