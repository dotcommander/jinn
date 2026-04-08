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
	result, err := e.searchFiles(args("pattern", "hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "a.go") {
		t.Errorf("expected a.go in results, got: %s", result)
	}
}

func TestSearchFiles_InvalidRegex(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.searchFiles(args("pattern", "[invalid"))
	if err == nil || !strings.Contains(err.Error(), "invalid regex") {
		t.Errorf("expected 'invalid regex' error, got: %v", err)
	}
}

func TestSearchFiles_CaseInsensitive(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "ci.txt", "Hello World\n")
	result, err := e.searchFiles(args("pattern", "hello", "case_insensitive", true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Hello") {
		t.Errorf("expected case-insensitive match, got: %s", result)
	}
}

func TestSearchFiles_IncludeFilter(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "needle.go", "needle\n")
	writeTestFile(t, dir, "needle.txt", "needle\n")
	result, err := e.searchFiles(args("pattern", "needle", "include", "*.go"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "needle.go") {
		t.Errorf("expected needle.go, got: %s", result)
	}
	if strings.Contains(result, "needle.txt") {
		t.Errorf("needle.txt should be excluded, got: %s", result)
	}
}
