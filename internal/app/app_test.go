package app

import (
	"flag"
	"path/filepath"
	"testing"

	"github.com/1001encore/wave/internal/store"
)

func TestDeriveChunkSymbolLinksIncludesPrimaryAndOccurrenceRoles(t *testing.T) {
	chunks := []store.ChunkData{
		{
			Key:           "chunk:handler",
			FilePath:      "svc.py",
			Kind:          "function_definition",
			Name:          "handler",
			StartLine:     10,
			StartCol:      0,
			EndLine:       20,
			EndCol:        0,
			PrimarySymbol: "pkg handler()",
		},
	}
	occurrences := []store.OccurrenceData{
		{
			FilePath:   "svc.py",
			Symbol:     "dep client",
			StartLine:  12,
			StartCol:   4,
			EndLine:    12,
			EndCol:     10,
			SyntaxKind: "identifier",
			IsRead:     true,
		},
		{
			FilePath:   "svc.py",
			Symbol:     "dep config",
			StartLine:  13,
			StartCol:   4,
			EndLine:    13,
			EndCol:     10,
			SyntaxKind: "identifier",
			IsWrite:    true,
		},
	}

	links := deriveChunkSymbolLinks(chunks, occurrences)
	got := map[string]float64{}
	for _, link := range links {
		got[link.Symbol+"|"+link.Role] = link.Weight
	}

	if got["pkg handler()|defines"] != 1.0 {
		t.Fatalf("primary symbol define weight = %v, want 1.0", got["pkg handler()|defines"])
	}
	if got["dep client|reads"] != 0.6 {
		t.Fatalf("read link weight = %v, want 0.6", got["dep client|reads"])
	}
	if got["dep client|uses"] != 0.55 {
		t.Fatalf("use link weight = %v, want 0.55", got["dep client|uses"])
	}
	if got["dep config|writes"] != 0.75 {
		t.Fatalf("write link weight = %v, want 0.75", got["dep config|writes"])
	}
}

func TestDetectWorkspaceUnitsFindsPythonAndTypeScriptInSameRoot(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplepoly"))
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}

	detectedRoot, units, err := detectWorkspaceUnits(root)
	if err != nil {
		t.Fatalf("detect workspace units: %v", err)
	}
	if detectedRoot != root {
		t.Fatalf("detected root = %q, want %q", detectedRoot, root)
	}
	if len(units) != 2 {
		t.Fatalf("unit count = %d, want 2", len(units))
	}

	found := map[string]bool{}
	for _, item := range units {
		found[item.Unit.Language] = true
	}
	if !found["python"] || !found["typescript"] {
		t.Fatalf("detected languages = %#v, want python+typescript", found)
	}
}

func TestBindCommonFlagsDefaultsDeviceToCUDA(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cc := bindCommonFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if cc.device != "cuda" {
		t.Fatalf("device default = %q, want %q", cc.device, "cuda")
	}
}

func TestIndexerInstallSpecForAdapter(t *testing.T) {
	tests := []struct {
		adapterID string
		wantBin   string
		wantPkg   string
		wantOK    bool
	}{
		{adapterID: "python-scip", wantBin: "scip-python", wantPkg: "@sourcegraph/scip-python", wantOK: true},
		{adapterID: "typescript-scip", wantBin: "scip-typescript", wantPkg: "@sourcegraph/scip-typescript", wantOK: true},
		{adapterID: "unknown", wantOK: false},
	}

	for _, tt := range tests {
		got, ok := indexerInstallSpecForAdapter(tt.adapterID)
		if ok != tt.wantOK {
			t.Fatalf("adapter %q: ok = %v, want %v", tt.adapterID, ok, tt.wantOK)
		}
		if !tt.wantOK {
			continue
		}
		if got.Binary != tt.wantBin {
			t.Fatalf("adapter %q: binary = %q, want %q", tt.adapterID, got.Binary, tt.wantBin)
		}
		if got.NPMPackage != tt.wantPkg {
			t.Fatalf("adapter %q: npm package = %q, want %q", tt.adapterID, got.NPMPackage, tt.wantPkg)
		}
	}
}
