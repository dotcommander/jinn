package jinn

import (
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
