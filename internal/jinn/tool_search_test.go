package jinn

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSearchFiles_TextFormat(t *testing.T) {
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
	// Default format should NOT be valid JSON array
	if strings.HasPrefix(strings.TrimSpace(result), "[") {
		t.Errorf("text format should not return JSON array, got: %s", result)
	}
}

func TestSearchFiles_JSONFormat(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "src.go", "package main\nfunc target() int { return 42 }\n")

	result, err := e.searchFiles(args("pattern", "target", "format", "json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must be valid JSON array
	if !strings.HasPrefix(result, "[") {
		t.Fatalf("expected JSON array, got: %s", result)
	}

	var results []searchResult
	if err := json.Unmarshal([]byte(result), &results); err != nil {
		t.Fatalf("failed to parse JSON: %v\ninput: %s", err, result)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	r := results[0]
	// rg outputs ./src.go, grep outputs src.go — accept either
	if !strings.HasSuffix(r.File, "src.go") {
		t.Errorf("expected file ending in src.go, got: %s", r.File)
	}
	if r.Line == 0 {
		t.Errorf("expected non-zero line number, got: %d", r.Line)
	}
	if r.Text == "" {
		t.Error("expected non-empty text field")
	}
	if !strings.Contains(r.Text, "target") {
		t.Errorf("expected text to contain 'target', got: %q", r.Text)
	}
	// Column should be omitted (omitempty) when not using rg --column
	if r.Column != 0 {
		t.Errorf("expected column=0 (omitted), got: %d", r.Column)
	}
	// Context fields should be empty (omitempty) without -C
	if r.ContextBefore != "" {
		t.Errorf("expected empty context_before, got: %q", r.ContextBefore)
	}
	if r.ContextAfter != "" {
		t.Errorf("expected empty context_after, got: %q", r.ContextAfter)
	}
}

func TestSearchFiles_JSONWithContext(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "ctx.go", "line one\nline two\nMATCH_HERE\nline four\nline five\n")

	result, err := e.searchFiles(args("pattern", "MATCH_HERE", "format", "json", "context_lines", 1.0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var results []searchResult
	if err := json.Unmarshal([]byte(result), &results); err != nil {
		t.Fatalf("failed to parse JSON: %v\ninput: %s", err, result)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	r := results[0]
	if r.ContextBefore == "" {
		t.Error("expected non-empty context_before with context_lines=1")
	}
	if r.ContextAfter == "" {
		t.Error("expected non-empty context_after with context_lines=1")
	}
	if !strings.Contains(r.ContextBefore, "line two") {
		t.Errorf("expected 'line two' in context_before, got: %q", r.ContextBefore)
	}
	if !strings.Contains(r.ContextAfter, "line four") {
		t.Errorf("expected 'line four' in context_after, got: %q", r.ContextAfter)
	}
}

func TestSearchFiles_NoMatch(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "empty.go", "package main\n")

	result, err := e.searchFiles(args("pattern", "ZZZ_NO_SUCH_PATTERN_ZZZ", "format", "json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "[]" {
		t.Errorf("expected empty JSON array for no match, got: %s", result)
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

func TestParseSearchResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want int // expected number of results
	}{
		{
			name: "basic match",
			raw:  "file.txt:10:hello world\n",
			want: 1,
		},
		{
			name: "multiple files",
			raw:  "a.go:5:func foo() {}\nb.go:12:func bar() {}\n",
			want: 2,
		},
		{
			name: "group separator",
			raw:  "a.go:5:match one\n--\nb.go:10:match two\n",
			want: 2,
		},
		{
			name: "context lines with dash separator",
			raw:  "a.go-4-before\na.go:5:MATCH\na.go-6-after\n",
			want: 1,
		},
		{
			name: "empty input",
			raw:  "",
			want: 0,
		},
		{
			name: "rg column format",
			raw:  "file.go:42:10:some text\n",
			want: 1,
		},
		{
			name: "no match returns empty slice not nil",
			raw:  "Binary file foo.bin matches\n",
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			results := parseSearchResults(tc.raw)
			if len(results) != tc.want {
				t.Errorf("expected %d results, got %d (raw=%q)", tc.want, len(results), tc.raw)
			}
			// Verify nil safety: empty results should be an empty slice, not nil
			if tc.want == 0 && results == nil {
				t.Error("expected non-nil empty slice")
			}
		})
	}
}

func TestParseSearchResults_ContextFields(t *testing.T) {
	t.Parallel()
	// Context lines use '-' separator, match lines use ':'
	raw := "f.txt-4-before line\nf.txt:5:MATCH\nf.txt-6-after line\n"
	results := parseSearchResults(raw)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Line != 5 {
		t.Errorf("expected line 5, got %d", r.Line)
	}
	if !strings.Contains(r.ContextBefore, "before line") {
		t.Errorf("expected 'before line' in context_before, got %q", r.ContextBefore)
	}
	if !strings.Contains(r.ContextAfter, "after line") {
		t.Errorf("expected 'after line' in context_after, got %q", r.ContextAfter)
	}
}

func TestParseSearchResults_ColumnField(t *testing.T) {
	t.Parallel()
	raw := "f.go:42:10:code here\n"
	results := parseSearchResults(raw)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Line != 42 {
		t.Errorf("expected line 42, got %d", r.Line)
	}
	if r.Column != 10 {
		t.Errorf("expected column 10, got %d", r.Column)
	}
	if r.Text != "code here" {
		t.Errorf("expected text 'code here', got %q", r.Text)
	}
}

func TestParseSearchResults_MatchText(t *testing.T) {
	t.Parallel()
	// Standard grep format: file:line:text (leading ':' stripped)
	raw := "file.go:10:some text here\n"
	results := parseSearchResults(raw)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Text != "some text here" {
		t.Errorf("expected text 'some text here', got %q", r.Text)
	}
}
