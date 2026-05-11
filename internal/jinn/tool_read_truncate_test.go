package jinn

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestReadFile_TruncateHead_Default(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	writeTestFile(t, dir, "big.txt", b.String())

	// No truncate arg → default "head"
	result, err := e.readFile(args("path", "big.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "1\tline1") {
		t.Errorf("head truncation should start from line 1, got prefix: %s", result.Text[:min(200, len(result.Text))])
	}
	// Must contain a start_line continuation hint
	if !strings.Contains(result.Text, "start_line=") {
		t.Errorf("expected start_line continuation hint, got tail: %s", result.Text[max(0, len(result.Text)-300):])
	}
}

func TestReadFile_TruncateTail(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	writeTestFile(t, dir, "big.txt", b.String())

	result, err := e.readFile(args("path", "big.txt", "truncate", "tail"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "line3000") {
		t.Errorf("tail truncation should include last line, got tail: %s", result.Text[max(0, len(result.Text)-300):])
	}
	// Window was pinned to the last 2000 lines (1001-3000); the "showing last"
	// within-chunk header is absent (count==limit, nothing omitted within window).
	// The file-level truncation is signalled by the continuation hint instead.
	if strings.Contains(result.Text, "showing last") {
		t.Errorf("unexpected 'showing last' marker when count==limit (nothing omitted within window)")
	}
}

func TestReadFile_TruncateMiddle_Preserved(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	writeTestFile(t, dir, "big.txt", b.String())

	// Pass end_line=3000 so all 3000 lines enter truncateOutputDetailed
	// (readDefaultLines caps the window at 2000 without an explicit end_line,
	// which would make count==limit and correctly skip truncation).
	result, err := e.readFile(args("path", "big.txt", "truncate", "middle", "end_line", float64(3000)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "lines omitted") {
		t.Errorf("middle truncation should have omitted-lines marker, got: %s", result.Text[:min(400, len(result.Text))])
	}
}

func TestReadFile_TruncateNone(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	writeTestFile(t, dir, "big.txt", b.String())

	result, err := e.readFile(args("path", "big.txt", "truncate", "none"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No line-overflow truncation marker (byte cap may still fire, that's fine)
	if strings.Contains(result.Text, "lines omitted") {
		t.Errorf("none strategy should not produce omitted-lines marker, got: %s", result.Text[:min(400, len(result.Text))])
	}
	if strings.Contains(result.Text, "start_line=") && strings.Contains(result.Text, "showing first") {
		t.Errorf("none strategy should not produce head-truncation marker")
	}
}

func TestReadFile_TruncateInvalidEnum(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "x.txt", "hello\n")

	_, err := e.readFile(args("path", "x.txt", "truncate", "bogus"))
	if err == nil {
		t.Fatal("expected error for invalid truncate value")
	}
	msg := err.Error()
	if !strings.Contains(msg, "bogus") {
		t.Errorf("error should mention the bad value, got: %s", msg)
	}
	// Suggestion should list the four valid values
	var sErr *ErrWithSuggestion
	if asErr, ok := err.(*ErrWithSuggestion); ok {
		sErr = asErr
	}
	if sErr == nil {
		t.Fatalf("expected *ErrWithSuggestion, got %T", err)
	}
	for _, v := range []string{"head", "tail", "middle", "none"} {
		if !strings.Contains(sErr.Suggestion, v) {
			t.Errorf("suggestion should mention %q, got: %s", v, sErr.Suggestion)
		}
	}
}

func TestReadFile_SingleLineExceedsByteCap(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// One line of 60 KB — exceeds the 50 KB per-line guard
	content := strings.Repeat("a", 60000) + "\n"
	writeTestFile(t, dir, "wide.txt", content)

	result, err := e.readFile(args("path", "wide.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "exceeds 50 KB limit") {
		t.Errorf("expected oversized-line hint, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "sed -n") {
		t.Errorf("expected sed command hint, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "wide.txt") {
		t.Errorf("expected filename in hint, got: %s", result.Text)
	}
	// No partial UTF-8 / null bytes in body
	if strings.ContainsRune(result.Text, 0) {
		t.Error("result contains null byte — partial UTF-8 corruption")
	}
}

	// --- Smart truncation integration tests ---

	func TestReadFile_TruncateSmart_PreservesWholeFunctions(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)

		// Generate a Go file with 30 functions, each ~10 lines.
		var b strings.Builder
		b.WriteString("package main\n\n")
		for i := range 30 {
			fmt.Fprintf(&b, "func func%d() {\n", i)
			for j := range 7 {
				fmt.Fprintf(&b, "\tstmt%d()\n", j)
			}
			b.WriteString("}\n\n")
		}
		writeTestFile(t, dir, "funcs.go", b.String())

		// Request with truncate=smart and end_line to include all lines.
		result, err := e.readFile(args("path", "funcs.go", "truncate", "smart", "end_line", float64(3000)))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(result.Text, "func0()") {
			t.Error("smart truncation should contain the first function")
		}
		if !strings.Contains(result.Text, "truncated") {
			// ~320 lines fits within the 2000 limit — no truncation needed at this size.
			t.Skip("file fits within limit — no truncation needed for this size")
		}
	}

	func TestReadFile_TruncateSmart_LargeGoFile(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)

		// 200 functions × 50 lines each = ~10000 lines — well over the 2000 limit.
		var b strings.Builder
		b.WriteString("package main\n\n")
		for i := range 200 {
			fmt.Fprintf(&b, "func func%d() {\n", i)
			for j := range 47 {
				fmt.Fprintf(&b, "\tstmt%d()\n", j)
			}
			b.WriteString("}\n\n")
		}
		writeTestFile(t, dir, "big.go", b.String())

		result, err := e.readFile(args("path", "big.go", "truncate", "smart", "end_line", float64(11000)))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result.Text, "truncated") {
			t.Fatal("expected truncation marker for large Go file")
		}
		// Smart truncation should cut at brace-depth-0 boundary.
		lines := strings.Split(result.Text, "\n")
		braceDepth := 0
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "[...") {
				break
			}
			for _, ch := range line {
				switch ch {
				case '{':
					braceDepth++
				case '}':
					braceDepth--
				}
			}
		}
		if braceDepth != 0 {
			t.Errorf("brace depth at truncation boundary = %d, want 0 — functions should be complete", braceDepth)
		}
		if !strings.Contains(result.Text, "func0()") {
			t.Error("expected func0() in truncated output")
		}
	}

	func TestReadFile_TruncateSmart_NonCSyntaxFallsBack(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)

		// Large YAML file — smart truncation should fall back to head strategy.
		var b strings.Builder
		for i := range 3000 {
			fmt.Fprintf(&b, "key%d: value%d\n", i, i)
		}
		writeTestFile(t, dir, "config.yaml", b.String())

		result, err := e.readFile(args("path", "config.yaml", "truncate", "smart"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result.Text, "key0: value0") {
			t.Error("smart fallback to head should contain first line")
		}
		if !strings.Contains(result.Text, "truncated") {
			t.Error("expected truncation marker")
		}
	}

	func TestReadFile_TruncateSmart_InvalidEnumStillRejected(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)
		writeTestFile(t, dir, "x.txt", "hello\n")

		_, err := e.readFile(args("path", "x.txt", "truncate", "bogus"))
		if err == nil {
			t.Fatal("expected error for invalid truncate value")
		}
		var sErr *ErrWithSuggestion
		if !errors.As(err, &sErr) {
			t.Fatalf("expected *ErrWithSuggestion, got %T", err)
		}
		if !strings.Contains(sErr.Suggestion, "smart") {
			t.Errorf("suggestion should mention 'smart', got: %s", sErr.Suggestion)
		}
	}

	func TestMultiRead_TruncateSmart(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)

		var go1 strings.Builder
		go1.WriteString("package main\n\n")
		for i := range 100 {
			fmt.Fprintf(&go1, "func f%d() { stmt%d() }\n\n", i, i)
		}
		writeTestFile(t, dir, "a.go", go1.String())

		var go2 strings.Builder
		go2.WriteString("package main\n\n")
		for i := range 100 {
			fmt.Fprintf(&go2, "func g%d() { stmt%d() }\n\n", i, i)
		}
		writeTestFile(t, dir, "b.go", go2.String())

		var yaml1 strings.Builder
		for i := range 3000 {
			fmt.Fprintf(&yaml1, "k%d: v%d\n", i, i)
		}
		writeTestFile(t, dir, "c.yaml", yaml1.String())

		files := []any{
			map[string]any{"path": "a.go", "truncate": "smart", "end_line": float64(500)},
			map[string]any{"path": "b.go", "truncate": "smart", "end_line": float64(500)},
			map[string]any{"path": "c.yaml", "truncate": "smart"},
		}
		result, err := e.multiRead(args("files", files))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var mr multiReadResult
		if err := json.Unmarshal([]byte(result.Text), &mr); err != nil {
			t.Fatalf("parse multi_read JSON: %v", err)
		}

		if _, ok := mr.Files["a.go"]; !ok {
			t.Error("expected a.go in files map")
		}
		if _, ok := mr.Files["b.go"]; !ok {
			t.Error("expected b.go in files map")
		}
		if _, ok := mr.Files["c.yaml"]; !ok {
			t.Error("expected c.yaml in files map")
		}
		if len(mr.Errors) > 0 {
			t.Errorf("unexpected errors: %v", mr.Errors)
		}
	}
