package jinn

import (
	"strings"
	"testing"
)

// findViaFind is the POSIX fallback used when fd is unavailable.
// We test it directly by calling findViaFind on an engine (fd path doesn't matter
// for the fallback itself).

func TestFindViaFind_BasicGlob(t *testing.T) {
	t.Parallel()
	_, dir := testEngine(t)
	writeTestFile(t, dir, "a.go", "package main")
	writeTestFile(t, dir, "b.go", "package main")
	writeTestFile(t, dir, "c.ts", "export {}")

	e := New(dir, "dev") // real engine but we call findViaFind directly
	raw, backend := e.findViaFind("*.go", ".")
	if backend != "find" {
		t.Errorf("backend = %q, want 'find'", backend)
	}
	if !strings.Contains(raw, "a.go") || !strings.Contains(raw, "b.go") {
		t.Errorf("expected a.go and b.go in output, got: %q", raw)
	}
	if strings.Contains(raw, "c.ts") {
		t.Errorf("c.ts should not appear in *.go search, got: %q", raw)
	}
}

func TestFindViaFind_SlashPatternUsesPathFlag(t *testing.T) {
	t.Parallel()
	// Patterns with "/" switch findViaFind from -name to -path.
	// Use a wildcard that POSIX find supports for -path matching.
	_, dir := testEngine(t)
	writeTestFile(t, dir, "cmd/main.go", "package main")
	writeTestFile(t, dir, "pkg/lib.go", "package lib")

	e := New(dir, "dev")
	// "*/cmd/*.go" — a -path pattern that matches files under any "cmd" dir.
	raw, _ := e.findViaFind("*/cmd/*.go", ".")
	if !strings.Contains(raw, "main.go") {
		t.Errorf("expected main.go in output, got: %q", raw)
	}
	if strings.Contains(raw, "lib.go") {
		t.Errorf("lib.go should not appear in cmd filter, got: %q", raw)
	}
}

func TestFindViaFind_NoMatch(t *testing.T) {
	t.Parallel()
	_, dir := testEngine(t)
	writeTestFile(t, dir, "foo.go", "package main")

	e := New(dir, "dev")
	raw, backend := e.findViaFind("*.xyz", ".")
	if backend != "find" {
		t.Errorf("backend = %q, want 'find'", backend)
	}
	if strings.TrimSpace(raw) != "" {
		t.Errorf("expected empty output for no-match, got: %q", raw)
	}
}

func TestFindFiles_FallbackToFind(t *testing.T) {
	t.Parallel()
	_, dir := testEngine(t)
	writeTestFile(t, dir, "main.go", "package main")

	// Engine with fdPath cleared to force the find fallback.
	e := New(dir, "dev")
	e.fdPath = ""

	result, err := e.findFiles(args("pattern", "*.go"))
	if err != nil {
		t.Fatalf("findFiles: %v", err)
	}
	res := parseFindResult(t, result)
	if res.Backend != "find" {
		t.Errorf("backend = %q, want 'find'", res.Backend)
	}
	if len(res.Files) == 0 {
		t.Error("expected at least one file")
	}
}

func TestFindFiles_FallbackToFind_NoMatch(t *testing.T) {
	t.Parallel()
	_, dir := testEngine(t)
	writeTestFile(t, dir, "main.go", "package main")

	e := New(dir, "dev")
	e.fdPath = ""

	result, err := e.findFiles(args("pattern", "*.xyz"))
	if err != nil {
		t.Fatalf("findFiles: %v", err)
	}
	res := parseFindResult(t, result)
	if res.Backend != "find" {
		t.Errorf("backend = %q, want 'find'", res.Backend)
	}
	if len(res.Files) != 0 {
		t.Errorf("expected 0 files, got %d: %v", len(res.Files), res.Files)
	}
}

func TestFindFiles_FallbackToFind_Truncation(t *testing.T) {
	t.Parallel()
	_, dir := testEngine(t)
	for i := 0; i < 5; i++ {
		writeTestFile(t, dir, "file_"+string(rune('a'+i))+".txt", "x")
	}

	e := New(dir, "dev")
	e.fdPath = ""

	// limit=3 with 5 matching files → truncated
	result, err := e.findFiles(args("pattern", "*.txt", "limit", float64(3)))
	if err != nil {
		t.Fatalf("findFiles: %v", err)
	}
	res := parseFindResult(t, result)
	if !res.Truncated {
		t.Error("expected truncated=true")
	}
	if len(res.Files) != 3 {
		t.Errorf("expected 3 files shown, got %d", len(res.Files))
	}
}
