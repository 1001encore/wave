package golang

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAdapterDetectAndSourceFiles(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplego"))
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}

	adapter := Adapter{}
	unit, err := adapter.Detect(root)
	if err != nil {
		t.Fatalf("detect go unit: %v", err)
	}

	if unit.Language != language {
		t.Fatalf("language = %q, want %q", unit.Language, language)
	}
	if unit.AdapterID != adapterID {
		t.Fatalf("adapter id = %q, want %q", unit.AdapterID, adapterID)
	}
	if got, want := unit.ManifestPath, filepath.Join(root, "go.mod"); got != want {
		t.Fatalf("manifest path = %q, want %q", got, want)
	}

	files, err := adapter.SourceFiles(unit)
	if err != nil {
		t.Fatalf("list source files: %v", err)
	}

	want := []string{
		"main.go",
		"pkg/util.go",
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("source files = %#v, want %#v", files, want)
	}
}

func TestNormalizeDisplayName(t *testing.T) {
	got := Adapter{}.NormalizeDisplayName("scip-go gomod samplego 0.1.0 pkg/worker.go:Worker#Run().")
	if got != "Run" {
		t.Fatalf("normalize display name = %q, want %q", got, "Run")
	}
}

func TestSourceFilesSkipsTestdataDirectory(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "go.mod"), []byte("module example.com/test\n\ngo 1.23\n"))
	mustWriteFile(t, filepath.Join(root, "main.go"), []byte("package main\n"))
	mustWriteFile(t, filepath.Join(root, "pkg", "util.go"), []byte("package pkg\n"))
	mustWriteFile(t, filepath.Join(root, "testdata", "fixture.go"), []byte("package testdata\n"))

	adapter := Adapter{}
	unit, err := adapter.Detect(root)
	if err != nil {
		t.Fatalf("detect go unit: %v", err)
	}

	files, err := adapter.SourceFiles(unit)
	if err != nil {
		t.Fatalf("list source files: %v", err)
	}

	want := []string{
		"main.go",
		"pkg/util.go",
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("source files = %#v, want %#v", files, want)
	}
}

func mustWriteFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
