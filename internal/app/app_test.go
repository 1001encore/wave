package app

import (
	"bytes"
	"flag"
	"math"
	"path/filepath"
	"strings"
	"testing"

	queryrouter "github.com/1001encore/wave/internal/query"
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

func TestDetectWorkspaceUnitsIncludesPythonAndTypeScriptInMixedRoot(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sampleall"))
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
	if len(units) < 2 {
		t.Fatalf("unit count = %d, want at least 2", len(units))
	}

	found := map[string]bool{}
	for _, item := range units {
		found[item.Unit.Language] = true
	}
	if !found["python"] || !found["typescript"] {
		t.Fatalf("detected languages = %#v, want python+typescript", found)
	}
}

func TestDetectWorkspaceUnitsFindsAllSupportedLanguagesInSameRoot(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sampleall"))
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
	if len(units) != 5 {
		t.Fatalf("unit count = %d, want 5", len(units))
	}

	found := map[string]bool{}
	for _, item := range units {
		found[item.Unit.Language] = true
	}
	for _, want := range []string{"go", "java", "python", "rust", "typescript"} {
		if !found[want] {
			t.Fatalf("missing language %q in detected set %#v", want, found)
		}
	}
}

func TestDetectWorkspaceUnitsPrefersMoreSpecificRootOnTie(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "samplejava"))
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}

	detectedRoot, _, err := detectWorkspaceUnits(root)
	if err != nil {
		t.Fatalf("detect workspace units: %v", err)
	}
	if detectedRoot != root {
		t.Fatalf("detected root = %q, want %q", detectedRoot, root)
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
	if cc.limit != defaultResultLimit {
		t.Fatalf("limit default = %d, want %d", cc.limit, defaultResultLimit)
	}
}

func TestBindCommonFlagsWithLimitOverridesDefault(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cc := bindCommonFlagsWithLimit(fs, 3)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	if cc.limit != 3 {
		t.Fatalf("limit default = %d, want %d", cc.limit, 3)
	}
}

func TestIndexerInstallSpecForAdapter(t *testing.T) {
	tests := []struct {
		adapterID  string
		wantBin    string
		wantMethod string
		wantPkg    string
		wantOK     bool
	}{
		{adapterID: "python-scip", wantBin: "scip-python", wantMethod: "npm", wantPkg: "@sourcegraph/scip-python", wantOK: true},
		{adapterID: "typescript-scip", wantBin: "scip-typescript", wantMethod: "npm", wantPkg: "@sourcegraph/scip-typescript", wantOK: true},
		{adapterID: "go-scip", wantBin: "scip-go", wantMethod: "go-install", wantOK: true},
		{adapterID: "rust-scip", wantBin: "rust-analyzer", wantMethod: "rustup-component", wantOK: true},
		{adapterID: "java-scip", wantBin: "scip-java", wantMethod: "coursier-bootstrap", wantOK: true},
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
		if got.Method != tt.wantMethod {
			t.Fatalf("adapter %q: method = %q, want %q", tt.adapterID, got.Method, tt.wantMethod)
		}
		if got.NPMPackage != tt.wantPkg {
			t.Fatalf("adapter %q: npm package = %q, want %q", tt.adapterID, got.NPMPackage, tt.wantPkg)
		}
	}
}

func TestWriteDefinitionOutputShowsAlternates(t *testing.T) {
	def := &store.DefinitionResult{
		SymbolID:    1,
		DisplayName: "greet",
		Kind:        "function",
		Path:        "/tmp/a.py",
		StartLine:   0,
		StartCol:    0,
		DocSummary:  "python",
	}
	result := queryrouter.DefinitionResult{
		Definition: def,
		Candidates: []store.DefinitionResult{
			*def,
			{SymbolID: 2, DisplayName: "greet", Kind: "function", Path: "/tmp/b.ts", StartLine: 10, StartCol: 3},
			{SymbolID: 3, DisplayName: "greet", Kind: "function", Path: "/tmp/c.rb", StartLine: 20, StartCol: 5},
			{SymbolID: 4, DisplayName: "greet", Kind: "function", Path: "/tmp/d.go", StartLine: 30, StartCol: 7},
		},
	}

	var buf bytes.Buffer
	writeDefinitionOutput(&buf, "greet", result, true, 3)
	out := buf.String()

	if !strings.Contains(out, "Definition: greet [function]") {
		t.Fatalf("output missing primary definition:\n%s", out)
	}
	if !strings.Contains(out, "---") {
		t.Fatalf("output missing divider:\n%s", out)
	}
	if !strings.Contains(out, "Other matches for \"greet\":") {
		t.Fatalf("output missing alternate heading:\n%s", out)
	}
	if !strings.Contains(out, "/tmp/b.ts:11:4") || !strings.Contains(out, "/tmp/c.rb:21:6") || !strings.Contains(out, "/tmp/d.go:31:8") {
		t.Fatalf("output missing alternates:\n%s", out)
	}
}

func TestWriteDefinitionOutputRespectsAlternateLimit(t *testing.T) {
	def := &store.DefinitionResult{
		SymbolID:    1,
		DisplayName: "greet",
		Kind:        "function",
		Path:        "/tmp/a.py",
	}
	result := queryrouter.DefinitionResult{
		Definition: def,
		Candidates: []store.DefinitionResult{
			*def,
			{SymbolID: 2, DisplayName: "greet", Kind: "function", Path: "/tmp/b.ts", StartLine: 10, StartCol: 3},
			{SymbolID: 3, DisplayName: "greet", Kind: "function", Path: "/tmp/c.rb", StartLine: 20, StartCol: 5},
			{SymbolID: 4, DisplayName: "greet", Kind: "function", Path: "/tmp/d.go", StartLine: 30, StartCol: 7},
		},
	}

	var buf bytes.Buffer
	writeDefinitionOutput(&buf, "greet", result, false, 2)
	out := buf.String()

	if !strings.Contains(out, "/tmp/b.ts:11:4") || !strings.Contains(out, "/tmp/c.rb:21:6") {
		t.Fatalf("output missing expected alternates:\n%s", out)
	}
	if strings.Contains(out, "/tmp/d.go:31:8") {
		t.Fatalf("output should be limited to first 2 alternates:\n%s", out)
	}
	if !strings.Contains(out, "... and 1 more") {
		t.Fatalf("output missing overflow count:\n%s", out)
	}
}

func TestDefinitionJSONPayloadIncludesCandidatesByDefault(t *testing.T) {
	result := queryrouter.DefinitionResult{
		Definition: &store.DefinitionResult{SymbolID: 1, DisplayName: "greet"},
		Candidates: []store.DefinitionResult{
			{SymbolID: 1, DisplayName: "greet"},
			{SymbolID: 2, DisplayName: "greet"},
		},
	}

	payload := definitionJSONPayload(result, false)
	got, ok := payload.(queryrouter.DefinitionResult)
	if !ok {
		t.Fatalf("payload type = %T, want queryrouter.DefinitionResult", payload)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("candidate count = %d, want 2", len(got.Candidates))
	}
}

func TestSearchHitsOutputOmitsScoresByDefault(t *testing.T) {
	hits := []store.SearchHit{
		{
			ChunkID:         7,
			FileID:          11,
			PrimarySymbolID: 13,
			Path:            "/tmp/a.go",
			StartLine:       1,
			EndLine:         2,
			Kind:            "function_definition",
			Name:            "run",
			HeaderText:      "func run()",
			Text:            "func run() {}",
			Score:           85.0,
		},
	}

	out := searchHitsOutput(hits, false, false)
	if len(out) != 1 {
		t.Fatalf("hit count = %d, want 1", len(out))
	}
	if out[0].Score != nil {
		t.Fatalf("score should be omitted by default, got %v", *out[0].Score)
	}
	if out[0].SoftmaxProbability != nil {
		t.Fatalf("softmax_probability should be omitted by default, got %v", *out[0].SoftmaxProbability)
	}
}

func TestSearchHitsOutputIncludesRawAndSoftmaxWhenEnabled(t *testing.T) {
	hits := []store.SearchHit{
		{ChunkID: 1, Score: 3.0},
		{ChunkID: 2, Score: 1.0},
		{ChunkID: 3, Score: 0.2},
	}

	out := searchHitsOutput(hits, true, true)
	if len(out) != 3 {
		t.Fatalf("hit count = %d, want 3", len(out))
	}
	if out[0].Score == nil || *out[0].Score != 3.0 {
		t.Fatalf("first raw score = %#v, want 3.0", out[0].Score)
	}
	if out[0].SoftmaxProbability == nil {
		t.Fatal("first softmax probability should be populated")
	}
	if out[1].SoftmaxProbability == nil || out[2].SoftmaxProbability == nil {
		t.Fatal("softmax probability should be populated for all hits")
	}
	if *out[0].SoftmaxProbability <= *out[1].SoftmaxProbability || *out[1].SoftmaxProbability <= *out[2].SoftmaxProbability {
		t.Fatalf(
			"expected descending softmax probabilities, got %.6f %.6f %.6f",
			*out[0].SoftmaxProbability,
			*out[1].SoftmaxProbability,
			*out[2].SoftmaxProbability,
		)
	}
}

func TestSoftmaxProbabilitiesNormalizeAndStayRelative(t *testing.T) {
	hits := []store.SearchHit{
		{Score: 3.0},
		{Score: 1.0},
		{Score: 0.2},
	}

	got := softmaxProbabilities(hits)
	if len(got) != len(hits) {
		t.Fatalf("probability count = %d, want %d", len(got), len(hits))
	}
	sum := 0.0
	for _, p := range got {
		sum += p
	}
	if math.Abs(sum-1.0) > 1e-9 {
		t.Fatalf("probability sum = %.12f, want 1.0", sum)
	}
	if got[0] < 0.80 || got[0] > 0.90 {
		t.Fatalf("first probability = %.6f, expected near 0.83 for 3/1/0.2 inputs", got[0])
	}
}
