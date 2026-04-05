package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIsIgnoredDirRule(t *testing.T) {
	rules := []ignoreRule{{pattern: "vendor", isDir: true}}
	if !isIgnored("vendor/dep.go", rules) {
		t.Fatal("expected vendor/dep.go to be ignored")
	}
	if !isIgnored("a/vendor/dep.go", rules) {
		t.Fatal("expected nested vendor path to be ignored")
	}
	if isIgnored("main.go", rules) {
		t.Fatal("main.go should not be ignored")
	}
}

func TestIsIgnoredGlobRule(t *testing.T) {
	rules := []ignoreRule{{pattern: "*.generated.go"}}
	if !isIgnored("pkg/schema.generated.go", rules) {
		t.Fatal("expected glob to match generated file")
	}
	if isIgnored("pkg/schema.go", rules) {
		t.Fatal("schema.go should not match *.generated.go")
	}
}

func TestIsIgnoredBareNameRule(t *testing.T) {
	rules := []ignoreRule{{pattern: "testdata"}}
	if !isIgnored("testdata/fixture.go", rules) {
		t.Fatal("expected testdata/ path to be ignored by bare name")
	}
	if !isIgnored("pkg/testdata/fixture.go", rules) {
		t.Fatal("expected nested testdata path to be ignored")
	}
}

func TestIsIgnoredPathPrefixRule(t *testing.T) {
	rules := []ignoreRule{{pattern: "docs/internal", isDir: true}}
	if !isIgnored("docs/internal/secret.md", rules) {
		t.Fatal("expected path prefix to match")
	}
	if isIgnored("docs/public/readme.md", rules) {
		t.Fatal("docs/public should not match docs/internal")
	}
}

func TestIsIgnoredCommentAndBlankLines(t *testing.T) {
	rules := loadWaveignore("/nonexistent/path")
	if rules != nil {
		t.Fatal("expected nil rules for missing file")
	}
}

func TestLoadWaveignore(t *testing.T) {
	root := t.TempDir()
	content := "# This is a comment\n\nvendor/\n*.generated.go\ntestdata\ndocs/internal/\n"
	if err := os.WriteFile(filepath.Join(root, ".waveignore"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	rules := loadWaveignore(root)
	want := []ignoreRule{
		{pattern: "vendor", isDir: true},
		{pattern: "*.generated.go", isDir: false},
		{pattern: "testdata", isDir: false},
		{pattern: "docs/internal", isDir: true},
	}
	if !reflect.DeepEqual(rules, want) {
		t.Fatalf("rules = %+v, want %+v", rules, want)
	}
}

func TestFilterIgnored(t *testing.T) {
	rules := []ignoreRule{
		{pattern: "vendor", isDir: true},
		{pattern: "*.generated.go", isDir: false},
	}
	files := []string{
		"main.go",
		"pkg/lib.go",
		"pkg/schema.generated.go",
		"vendor/dep.go",
	}
	got := filterIgnored(files, rules)
	want := []string{"main.go", "pkg/lib.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered = %v, want %v", got, want)
	}
}

func TestFilterIgnoredNilRules(t *testing.T) {
	files := []string{"a.go", "b.go"}
	got := filterIgnored(files, nil)
	if !reflect.DeepEqual(got, files) {
		t.Fatalf("expected no filtering with nil rules")
	}
}

func TestSourceFilesRespectsWaveignoreWithGit(t *testing.T) {
	root := t.TempDir()
	mustGitInitWI(t, root)

	mustWriteFileWI(t, filepath.Join(root, "main.go"), []byte("package main\n"))
	mustWriteFileWI(t, filepath.Join(root, "pkg", "lib.go"), []byte("package pkg\n"))
	mustWriteFileWI(t, filepath.Join(root, "generated", "gen.go"), []byte("package gen\n"))
	mustWriteFileWI(t, filepath.Join(root, ".waveignore"), []byte("generated/\n"))
	mustGitAddWI(t, root, ".")

	files, err := SourceFiles(root, []string{".go"}, nil)
	if err != nil {
		t.Fatalf("SourceFiles: %v", err)
	}

	want := []string{"main.go", "pkg/lib.go"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
}

func TestSourceFilesRespectsWaveignoreWithoutGit(t *testing.T) {
	root := t.TempDir()

	mustWriteFileWI(t, filepath.Join(root, "main.go"), []byte("package main\n"))
	mustWriteFileWI(t, filepath.Join(root, "pkg", "lib.go"), []byte("package pkg\n"))
	mustWriteFileWI(t, filepath.Join(root, "generated", "gen.go"), []byte("package gen\n"))
	mustWriteFileWI(t, filepath.Join(root, ".waveignore"), []byte("generated/\n"))

	files, err := SourceFiles(root, []string{".go"}, []string{".git", ".wave"})
	if err != nil {
		t.Fatalf("SourceFiles: %v", err)
	}

	want := []string{"main.go", "pkg/lib.go"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
}

func TestSourceFilesWaveignoreGlobPattern(t *testing.T) {
	root := t.TempDir()

	mustWriteFileWI(t, filepath.Join(root, "main.go"), []byte("package main\n"))
	mustWriteFileWI(t, filepath.Join(root, "schema.generated.go"), []byte("package main\n"))
	mustWriteFileWI(t, filepath.Join(root, "pkg", "types.generated.go"), []byte("package pkg\n"))
	mustWriteFileWI(t, filepath.Join(root, "pkg", "real.go"), []byte("package pkg\n"))
	mustWriteFileWI(t, filepath.Join(root, ".waveignore"), []byte("*.generated.go\n"))

	files, err := SourceFiles(root, []string{".go"}, []string{".git", ".wave"})
	if err != nil {
		t.Fatalf("SourceFiles: %v", err)
	}

	want := []string{"main.go", "pkg/real.go"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
}

func mustGitInitWI(t *testing.T, dir string) {
	t.Helper()
	runWI(t, dir, "git", "init")
	runWI(t, dir, "git", "config", "user.email", "test@test.com")
	runWI(t, dir, "git", "config", "user.name", "Test")
}

func mustGitAddWI(t *testing.T, dir string, paths ...string) {
	t.Helper()
	args := append([]string{"add"}, paths...)
	runWI(t, dir, "git", args...)
}

func runWI(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func mustWriteFileWI(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
