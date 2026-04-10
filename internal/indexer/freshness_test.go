package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/1001encore/wave/internal/store"
	"github.com/1001encore/wave/internal/syntax"
	"github.com/1001encore/wave/internal/vcs"
	"github.com/1001encore/wave/internal/workspace"
)

type freshnessTestAdapter struct {
	id         string
	language   string
	extensions []string
}

func (f freshnessTestAdapter) ID() string { return f.id }
func (f freshnessTestAdapter) Language() string {
	return f.language
}
func (f freshnessTestAdapter) Detect(string) (workspace.Unit, error) { return workspace.Unit{}, nil }
func (f freshnessTestAdapter) Validate(context.Context, workspace.Unit) error {
	return nil
}
func (f freshnessTestAdapter) Index(context.Context, workspace.Unit, string) (Result, error) {
	return Result{}, nil
}
func (f freshnessTestAdapter) SourceFiles(unit workspace.Unit) ([]string, error) {
	return vcs.SourceFiles(unit.RootPath, f.extensions, []string{".git", ".wave", "vendor", "bin", "testdata"})
}
func (f freshnessTestAdapter) SyntaxExtractor() syntax.Extractor { return nil }
func (f freshnessTestAdapter) DeriveEdges(context.Context, DeriveRequest) ([]store.EdgeData, error) {
	return nil, nil
}
func (f freshnessTestAdapter) NormalizeDisplayName(value string) string { return value }
func (f freshnessTestAdapter) Manifests() []string                      { return nil }

func TestComputeFreshnessGitDiffUsesRealCounts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	adapter := freshnessTestAdapter{id: "go-scip", language: "go", extensions: []string{".go"}}

	mustGitInitFreshness(t, root)
	mustWriteFreshness(t, filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"))
	mustGitAddFreshness(t, root, ".")
	mustGitCommitFreshness(t, root, "initial")
	indexedHash := mustHeadFreshness(t, root)

	st := mustOpenStoreFreshness(t)
	defer st.Close()
	mustIndexSnapshot(t, ctx, st, root, adapter, indexedHash, "main.go")

	mustWriteFreshness(t, filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() { println(\"hi\") }\n"))
	mustGitAddFreshness(t, root, ".")
	mustGitCommitFreshness(t, root, "touch go file")

	fresh, err := ComputeFreshness(ctx, st, adapter, workspace.Unit{
		RootPath:  root,
		Language:  adapter.language,
		AdapterID: adapter.id,
	})
	if err != nil {
		t.Fatalf("compute freshness: %v", err)
	}

	if !fresh.Dirty {
		t.Fatal("expected stale index after committed source change")
	}
	if fresh.ChangedFiles != 1 || fresh.DirtyFiles != 1 || fresh.NewFiles != 0 || fresh.MissingFiles != 0 {
		t.Fatalf("unexpected freshness counts: %+v", fresh)
	}
	if fresh.LineRatio <= 0 {
		t.Fatalf("expected positive changed LOC ratio, got %+v", fresh)
	}
	if fresh.IndexedFiles != 1 || fresh.CurrentFiles != 1 {
		t.Fatalf("unexpected file totals: indexed=%d current=%d", fresh.IndexedFiles, fresh.CurrentFiles)
	}
}

func TestComputeFreshnessGitDiffIgnoresIrrelevantFileChanges(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	adapter := freshnessTestAdapter{id: "go-scip", language: "go", extensions: []string{".go"}}

	mustGitInitFreshness(t, root)
	mustWriteFreshness(t, filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"))
	mustGitAddFreshness(t, root, ".")
	mustGitCommitFreshness(t, root, "initial")
	indexedHash := mustHeadFreshness(t, root)

	st := mustOpenStoreFreshness(t)
	defer st.Close()
	mustIndexSnapshot(t, ctx, st, root, adapter, indexedHash, "main.go")

	mustWriteFreshness(t, filepath.Join(root, "README.md"), []byte("# docs only change\n"))
	mustGitAddFreshness(t, root, ".")
	mustGitCommitFreshness(t, root, "readme only")

	fresh, err := ComputeFreshness(ctx, st, adapter, workspace.Unit{
		RootPath:  root,
		Language:  adapter.language,
		AdapterID: adapter.id,
	})
	if err != nil {
		t.Fatalf("compute freshness: %v", err)
	}

	if fresh.Dirty {
		t.Fatalf("expected clean index when only non-source files changed: %+v", fresh)
	}
	if fresh.ChangedFiles != 0 {
		t.Fatalf("expected zero changed source files, got %d", fresh.ChangedFiles)
	}
}

func TestComputeFreshnessGitDiffIncludesUntrackedSourceFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	adapter := freshnessTestAdapter{id: "go-scip", language: "go", extensions: []string{".go"}}

	mustGitInitFreshness(t, root)
	mustWriteFreshness(t, filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"))
	mustGitAddFreshness(t, root, ".")
	mustGitCommitFreshness(t, root, "initial")
	indexedHash := mustHeadFreshness(t, root)

	st := mustOpenStoreFreshness(t)
	defer st.Close()
	mustIndexSnapshot(t, ctx, st, root, adapter, indexedHash, "main.go")

	mustWriteFreshness(t, filepath.Join(root, "new.go"), []byte("package main\n\nfunc helper() {}\n"))

	fresh, err := ComputeFreshness(ctx, st, adapter, workspace.Unit{
		RootPath:  root,
		Language:  adapter.language,
		AdapterID: adapter.id,
	})
	if err != nil {
		t.Fatalf("compute freshness: %v", err)
	}

	if !fresh.Dirty {
		t.Fatal("expected stale index after adding untracked source file")
	}
	if fresh.NewFiles != 1 || fresh.ChangedFiles != 1 {
		t.Fatalf("expected one new source file, got %+v", fresh)
	}
}

func TestComputeFreshnessGitDiffRenameOnlyIsCleanUnderLOCModel(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	adapter := freshnessTestAdapter{id: "go-scip", language: "go", extensions: []string{".go"}}

	mustGitInitFreshness(t, root)
	mustWriteFreshness(t, filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"))
	mustGitAddFreshness(t, root, ".")
	mustGitCommitFreshness(t, root, "initial")
	indexedHash := mustHeadFreshness(t, root)

	st := mustOpenStoreFreshness(t)
	defer st.Close()
	mustIndexSnapshot(t, ctx, st, root, adapter, indexedHash, "main.go")

	if err := os.Rename(filepath.Join(root, "main.go"), filepath.Join(root, "renamed.go")); err != nil {
		t.Fatalf("rename file: %v", err)
	}
	mustGitAddFreshness(t, root, "-A")
	mustGitCommitFreshness(t, root, "rename only")

	fresh, err := ComputeFreshness(ctx, st, adapter, workspace.Unit{
		RootPath:  root,
		Language:  adapter.language,
		AdapterID: adapter.id,
	})
	if err != nil {
		t.Fatalf("compute freshness: %v", err)
	}

	if fresh.Dirty {
		t.Fatalf("expected clean index for rename-only change under LOC model: %+v", fresh)
	}
	if fresh.LineRatio != 0 || fresh.ChangedLines != 0 {
		t.Fatalf("expected zero LOC deltas for rename-only change: %+v", fresh)
	}
}

func mustOpenStoreFreshness(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "wave.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return st
}

func mustIndexSnapshot(t *testing.T, ctx context.Context, st *store.Store, root string, adapter freshnessTestAdapter, gitHash string, relPaths ...string) {
	t.Helper()
	files := make([]store.FileData, 0, len(relPaths))
	for _, relPath := range relPaths {
		absPath := filepath.Join(root, relPath)
		content, err := os.ReadFile(absPath)
		if err != nil {
			t.Fatalf("read %s: %v", relPath, err)
		}
		files = append(files, store.FileData{
			RelativePath: relPath,
			AbsPath:      absPath,
			Language:     adapter.language,
			ContentHash:  sha256HexFreshness(content),
			LineCount:    countLinesFreshness(content),
		})
	}

	payload := store.IndexPayload{
		Project: store.ProjectData{
			RootPath:          root,
			Name:              "freshness-test",
			Language:          adapter.language,
			ManifestPath:      filepath.Join(root, "go.mod"),
			EnvironmentSource: "",
			AdapterID:         adapter.id,
			ScipArtifactPath:  filepath.Join(root, ".wave", "artifacts", "index."+adapter.id+".scip"),
			ToolName:          "test-indexer",
			ToolVersion:       "test",
			GitCommitHash:     gitHash,
		},
		Files: files,
	}
	if err := st.ReplaceProjectIndex(ctx, payload); err != nil {
		t.Fatalf("replace project index: %v", err)
	}
}

func sha256HexFreshness(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func countLinesFreshness(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	lines := 0
	for _, b := range content {
		if b == '\n' {
			lines++
		}
	}
	if content[len(content)-1] != '\n' {
		lines++
	}
	return lines
}

func mustGitInitFreshness(t *testing.T, dir string) {
	t.Helper()
	runFreshness(t, dir, "git", "init")
	runFreshness(t, dir, "git", "config", "user.email", "test@test.com")
	runFreshness(t, dir, "git", "config", "user.name", "Test")
}

func mustGitAddFreshness(t *testing.T, dir string, paths ...string) {
	t.Helper()
	args := append([]string{"add"}, paths...)
	runFreshness(t, dir, "git", args...)
}

func mustGitCommitFreshness(t *testing.T, dir string, msg string) {
	t.Helper()
	runFreshness(t, dir, "git", "commit", "-m", msg)
}

func mustHeadFreshness(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func runFreshness(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func mustWriteFreshness(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
