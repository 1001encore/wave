package rust

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestAdapterDetectAndSourceFiles(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplerust"))
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}

	adapter := Adapter{}
	unit, err := adapter.Detect(root)
	if err != nil {
		t.Fatalf("detect rust unit: %v", err)
	}

	if unit.Language != language {
		t.Fatalf("language = %q, want %q", unit.Language, language)
	}
	if unit.AdapterID != adapterID {
		t.Fatalf("adapter id = %q, want %q", unit.AdapterID, adapterID)
	}
	if got, want := unit.ManifestPath, filepath.Join(root, "Cargo.toml"); got != want {
		t.Fatalf("manifest path = %q, want %q", got, want)
	}

	files, err := adapter.SourceFiles(unit)
	if err != nil {
		t.Fatalf("list source files: %v", err)
	}

	want := []string{
		"src/lib.rs",
		"src/main.rs",
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("source files = %#v, want %#v", files, want)
	}
}

func TestNormalizeDisplayName(t *testing.T) {
	got := Adapter{}.NormalizeDisplayName("rust-analyzer cargo samplerust 0.1.0 worker/Worker#run().")
	if got != "run" {
		t.Fatalf("normalize display name = %q, want %q", got, "run")
	}
}
