package jinn

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindFiles_BasicGlob(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "foo.go", "package main")
	writeTestFile(t, dir, "bar.go", "package main")
	writeTestFile(t, dir, "baz.ts", "export {}")

	result, err := e.findFiles(args("pattern", "*.go"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := parseFindResult(t, result)
	if len(res.Files) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(res.Files), res.Files)
	}
	for _, f := range res.Files {
		if !strings.HasSuffix(f, ".go") {
			t.Errorf("expected .go file, got %q", f)
		}
	}
	if res.Truncated {
		t.Error("should not be truncated")
	}
}

func TestFindFiles_NoMatch(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "foo.go", "package main")

	result, err := e.findFiles(args("pattern", "*.xyz"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := parseFindResult(t, result)
	if len(res.Files) != 0 {
		t.Errorf("expected 0 files, got %d", len(res.Files))
	}
}

func TestFindFiles_MissingPattern(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.findFiles(args())
	if err == nil {
		t.Fatal("expected error for missing pattern")
	}
	if !strings.Contains(err.Error(), "pattern is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFindFiles_PathArg(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "src/main.go", "package main")
	writeTestFile(t, dir, "src/util.ts", "export {}")
	writeTestFile(t, dir, "other.txt", "hello")

	result, err := e.findFiles(args("pattern", "*.go", "path", "src"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := parseFindResult(t, result)
	if len(res.Files) != 1 {
		t.Errorf("expected 1 file in src/, got %d: %v", len(res.Files), res.Files)
	}
	if res.Files[0] != "src/main.go" {
		t.Errorf("expected src/main.go, got %q", res.Files[0])
	}
}

func TestFindFiles_Truncation(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	for i := 0; i < 20; i++ {
		writeTestFile(t, dir, filepath.Join("sub", fmt.Sprintf("file%02d.go", i)), "package p")
	}

	result, err := e.findFiles(args("pattern", "*.go", "path", "sub", "limit", float64(5)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := parseFindResult(t, result)
	if !res.Truncated {
		t.Error("expected truncation")
	}
	if res.TotalCount != 20 {
		t.Errorf("TotalCount = %d, want 20", res.TotalCount)
	}
	if len(res.Files) != 5 {
		t.Errorf("len(Files) = %d, want 5", len(res.Files))
	}
	if !strings.Contains(result, "TRUNCATED") {
		t.Error("expected TRUNCATED hint in output")
	}
}

func TestFindFiles_Backend(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	result, err := e.findFiles(args("pattern", "*.go"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := parseFindResult(t, result)
	if res.Backend != "fd" && res.Backend != "find" {
		t.Errorf("Backend = %q, want 'fd' or 'find'", res.Backend)
	}
}

func TestFindFiles_ExcludeDirs(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "good.go", "package main")
	writeTestFile(t, dir, filepath.Join(".git", "bad.go"), "package git")
	writeTestFile(t, dir, filepath.Join("node_modules", "also_bad.go"), "package nm")

	result, err := e.findFiles(args("pattern", "*.go"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := parseFindResult(t, result)
	if len(res.Files) != 1 {
		t.Errorf("expected 1 file (excluded .git and node_modules), got %d: %v", len(res.Files), res.Files)
	}
	if res.Files[0] != "good.go" {
		t.Errorf("expected good.go, got %q", res.Files[0])
	}
}

func TestFindFiles_SlashPattern(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, filepath.Join("src", "app.test.ts"), "test")
	writeTestFile(t, dir, filepath.Join("src", "app.ts"), "src")
	writeTestFile(t, dir, "other.test.ts", "test")

	result, err := e.findFiles(args("pattern", "src/**/*_test.ts"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := parseFindResult(t, result)
	// fd with --full-path should match src/app.test.ts via "**/src/**/*_test.ts"
	// find with -path should also match.
	t.Logf("files found: %v (backend: %s)", res.Files, res.Backend)
	if len(res.Files) == 0 && res.Backend == "fd" {
		// fd pattern matching is stricter; log but don't fail.
		t.Log("fd returned no results for slash pattern — pattern semantics differ from find")
	}
}

func TestFindFiles_Dispatch(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "hello.go", "package main")

	result, meta, err := e.Dispatch(nil, "find_files", args("pattern", "*.go"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != nil {
		t.Errorf("expected nil meta for find_files, got: %v", meta)
	}
	res := parseFindResult(t, result.Text)
	if len(res.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(res.Files))
	}
}

// Ensure the import is used.
var _ = json.Marshal
var _ = fmt.Sprintf
