package jinn

import (
	"strings"
	"testing"
)

func TestSearchFiles_Basic(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.go", "package main\nfunc hello() {}\n")
	writeTestFile(t, dir, "b.go", "package main\nfunc world() {}\n")
	result := e.searchFiles(args("pattern", "hello"))
	if !strings.Contains(result, "a.go") {
		t.Errorf("expected a.go in results, got: %s", result)
	}
}

func TestSearchFiles_InvalidRegex(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	result := e.searchFiles(args("pattern", "[invalid"))
	if !strings.Contains(result, "invalid regex") {
		t.Errorf("expected 'invalid regex', got: %s", result)
	}
}

func TestSearchFiles_CaseInsensitive(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "ci.txt", "Hello World\n")
	result := e.searchFiles(args("pattern", "hello", "case_insensitive", true))
	if !strings.Contains(result, "Hello") {
		t.Errorf("expected case-insensitive match, got: %s", result)
	}
}

func TestSearchFiles_IncludeFilter(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "needle.go", "needle\n")
	writeTestFile(t, dir, "needle.txt", "needle\n")
	result := e.searchFiles(args("pattern", "needle", "include", "*.go"))
	if !strings.Contains(result, "needle.go") {
		t.Errorf("expected needle.go, got: %s", result)
	}
	if strings.Contains(result, "needle.txt") {
		t.Errorf("needle.txt should be excluded, got: %s", result)
	}
}
