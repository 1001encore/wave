package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIsGitRepo(t *testing.T) {
	// The wave project root itself is a git repo.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if !IsGitRepo(root) {
		t.Fatal("expected wave project root to be a git repo")
	}

	tmp := t.TempDir()
	if IsGitRepo(tmp) {
		t.Fatal("expected temp dir to not be a git repo")
	}
}

func TestListFilesRespectsGitignore(t *testing.T) {
	root := t.TempDir()
	mustGitInit(t, root)

	mustWriteFile(t, filepath.Join(root, "main.go"), []byte("package main\n"))
	mustWriteFile(t, filepath.Join(root, "util.go"), []byte("package main\n"))
	mustWriteFile(t, filepath.Join(root, "vendor", "dep.go"), []byte("package dep\n"))
	mustWriteFile(t, filepath.Join(root, ".gitignore"), []byte("vendor/\n"))

	mustGitAdd(t, root, ".")

	files, err := ListFiles(root, []string{".go"})
	if err != nil {
		t.Fatalf("list files: %v", err)
	}

	want := []string{"main.go", "util.go"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
}

func TestListFilesIncludesUntrackedNonIgnored(t *testing.T) {
	root := t.TempDir()
	mustGitInit(t, root)

	mustWriteFile(t, filepath.Join(root, "tracked.go"), []byte("package main\n"))
	mustGitAdd(t, root, ".")

	// Create an untracked file that is NOT ignored.
	mustWriteFile(t, filepath.Join(root, "untracked.go"), []byte("package main\n"))

	files, err := ListFiles(root, []string{".go"})
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	want := []string{"tracked.go", "untracked.go"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
}

func TestWalkFilesFallback(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "main.go"), []byte("package main\n"))
	mustWriteFile(t, filepath.Join(root, "vendor", "dep.go"), []byte("package dep\n"))
	mustWriteFile(t, filepath.Join(root, "pkg", "lib.go"), []byte("package pkg\n"))

	files, err := WalkFiles(root, []string{".go"}, []string{"vendor", ".git", ".wave"})
	if err != nil {
		t.Fatalf("walk files: %v", err)
	}

	want := []string{"main.go", "pkg/lib.go"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
}

func TestSourceFilesUsesGitWhenAvailable(t *testing.T) {
	root := t.TempDir()
	mustGitInit(t, root)

	mustWriteFile(t, filepath.Join(root, "main.go"), []byte("package main\n"))
	mustWriteFile(t, filepath.Join(root, "vendor", "dep.go"), []byte("package dep\n"))
	mustWriteFile(t, filepath.Join(root, ".gitignore"), []byte("vendor/\n"))
	mustGitAdd(t, root, ".")

	files, err := SourceFiles(root, []string{".go"}, []string{"vendor", ".git", ".wave"})
	if err != nil {
		t.Fatalf("source files: %v", err)
	}

	want := []string{"main.go"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
}

func TestSourceFilesFallsBackWithoutGit(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "main.go"), []byte("package main\n"))
	mustWriteFile(t, filepath.Join(root, "vendor", "dep.go"), []byte("package dep\n"))

	files, err := SourceFiles(root, []string{".go"}, []string{"vendor", ".git", ".wave"})
	if err != nil {
		t.Fatalf("source files: %v", err)
	}

	want := []string{"main.go"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
}

func TestHeadCommit(t *testing.T) {
	root := t.TempDir()
	mustGitInit(t, root)
	mustWriteFile(t, filepath.Join(root, "main.go"), []byte("package main\n"))
	mustGitAdd(t, root, ".")
	mustGitCommit(t, root, "init")

	hash, err := HeadCommit(root)
	if err != nil {
		t.Fatalf("head commit: %v", err)
	}
	if len(hash) != 40 {
		t.Fatalf("expected 40-char hash, got %q", hash)
	}
}

func TestHasUncommittedChanges(t *testing.T) {
	root := t.TempDir()
	mustGitInit(t, root)
	mustWriteFile(t, filepath.Join(root, "main.go"), []byte("package main\n"))
	mustGitAdd(t, root, ".")
	mustGitCommit(t, root, "init")

	has, err := HasUncommittedChanges(root, []string{".go"})
	if err != nil {
		t.Fatalf("check uncommitted: %v", err)
	}
	if has {
		t.Fatal("expected no uncommitted changes after commit")
	}

	mustWriteFile(t, filepath.Join(root, "new.go"), []byte("package main\n"))
	has, err = HasUncommittedChanges(root, []string{".go"})
	if err != nil {
		t.Fatalf("check uncommitted: %v", err)
	}
	if !has {
		t.Fatal("expected uncommitted changes after adding new file")
	}
}

func TestMatchesExtensions(t *testing.T) {
	if !matchesExtensions("foo.go", []string{".go"}) {
		t.Fatal("expected .go to match")
	}
	if matchesExtensions("foo.py", []string{".go"}) {
		t.Fatal("expected .py to not match .go")
	}
	if !matchesExtensions("anything", nil) {
		t.Fatal("nil extensions should match everything")
	}
}

func mustGitInit(t *testing.T, dir string) {
	t.Helper()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")
}

func mustGitAdd(t *testing.T, dir string, paths ...string) {
	t.Helper()
	args := append([]string{"add"}, paths...)
	run(t, dir, "git", args...)
}

func mustGitCommit(t *testing.T, dir string, msg string) {
	t.Helper()
	run(t, dir, "git", "commit", "-m", msg)
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
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
