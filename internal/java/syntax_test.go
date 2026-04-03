package java

import (
	"context"
	"testing"

	"github.com/1001encore/wave/internal/syntax"
)

func TestSyntaxExtractorExtractsJavaChunks(t *testing.T) {
	source := []byte(`package com.example;

import java.util.List;

public class Greeter {
    private final String prefix = "Hi";

    public Greeter() {}

    public String greet(String name) {
        return prefix + " " + name;
    }
}
`)

	chunks, err := SyntaxExtractor{}.Extract(context.Background(), "Greeter.java", source)
	if err != nil {
		t.Fatalf("extract chunks: %v", err)
	}

	assertChunk(t, chunks, "module", "Greeter.java")
	assertChunk(t, chunks, "package_declaration", "package com.example;")
	assertChunk(t, chunks, "import_declaration", "import java.util.List;")
	assertChunk(t, chunks, "class_declaration", "Greeter")
	assertChunk(t, chunks, "constructor_declaration", "Greeter")
	assertChunk(t, chunks, "method_declaration", "greet")
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
