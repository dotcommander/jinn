package jinn

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditC201BackendFailureAndNoMatch(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "needle\n")
	for _, format := range []string{"text", "json", "filenames"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			req := searchRequest{cmd: filepath.Join(dir, "missing-grep"), pattern: "needle", searchPath: ".", maxMatches: 5}
			var err error
			if format == "filenames" {
				_, err = e.searchFilenamesOnly(context.Background(), req)
			} else {
				_, err = e.runSearch(context.Background(), req, 0)
			}
			if err == nil || !strings.Contains(err.Error(), "missing-grep") {
				t.Fatalf("expected backend start error, got %v", err)
			}
		})
	}
	if err := searchGrepFailure(grepResult{exitCode: 1}); err != nil {
		t.Fatalf("grep no-match must not fail: %v", err)
	}
	if err := searchGrepFailure(grepResult{exitCode: 2, stderr: "read error"}); err == nil || !strings.Contains(err.Error(), "read error") {
		t.Fatalf("expected read error, got %v", err)
	}
	match, err := e.searchFiles(args("pattern", "needle"))
	if err != nil || !strings.Contains(match, "a.txt") {
		t.Fatalf("normal match = %q, %v", match, err)
	}
	miss, err := e.searchFiles(args("pattern", "absent"))
	if err != nil || !strings.Contains(miss, "no matches") {
		t.Fatalf("normal miss = %q, %v", miss, err)
	}
}

func TestAuditC202HyphenatedContext(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "my-file.go", "before\nMATCH\nafter\n")
	got, err := e.searchFiles(args("pattern", "MATCH", "format", "json", "context_lines", 1.0))
	if err != nil {
		t.Fatal(err)
	}
	var result searchFilesResult
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 || result.Results[0].ContextBefore != "before\n" || result.Results[0].ContextAfter != "after\n" {
		t.Fatalf("context = %+v", result.Results)
	}
}

func TestAuditC203IncludeBeforeFileLimit(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	for i := range 60 {
		name := fmt.Sprintf("file-%03d.txt", i)
		if i < 3 {
			name = fmt.Sprintf("file-%03d.go", i)
		}
		writeTestFile(t, dir, name, "old\n")
	}
	base := args("files", "**/*", "include", "*.go", "pattern", "old", "replacement", "new", "dry_run", true)
	if _, err := e.searchReplace(context.Background(), base); err != nil {
		t.Fatalf("filtered candidates: %v", err)
	}
	for i := range 51 {
		writeTestFile(t, dir, fmt.Sprintf("extra-%03d.go", i), "old\n")
	}
	if _, err := e.searchReplace(context.Background(), base); err == nil || !strings.Contains(err.Error(), "too many files matched") {
		t.Fatalf("expected post-filter limit: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "file-000.go"))
	if err != nil || string(data) != "old\n" {
		t.Fatalf("dry run modified file: %q, %v", data, err)
	}
}
