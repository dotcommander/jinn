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

	// Result is now a JSON object with results/truncated/total_count fields.
	if !strings.HasPrefix(result, "{") {
		t.Fatalf("expected JSON object, got: %s", result)
	}

	var resp struct {
		Results    []searchResult `json:"results"`
		Truncated  bool           `json:"truncated"`
		TotalCount int            `json:"total_count"`
	}
	// Unmarshal only the first JSON object (hint may follow on next line).
	dec := json.NewDecoder(strings.NewReader(result))
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("failed to parse JSON: %v\ninput: %s", err, result)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one result")
	}

	r := resp.Results[0]
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

	var resp struct {
		Results []searchResult `json:"results"`
	}
	dec := json.NewDecoder(strings.NewReader(result))
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("failed to parse JSON: %v\ninput: %s", err, result)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one result")
	}

	r := resp.Results[0]
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

func TestSearchFiles_JSONWithContextOrder(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// alpha=line1, beta=line2, gamma=line3, TARGET=line4, delta=line5, epsilon=line6
	writeTestFile(t, dir, "order.go", "alpha\nbeta\ngamma\nTARGET\ndelta\nepsilon\n")

	result, err := e.searchFiles(args("pattern", "TARGET", "format", "json", "context_lines", 2.0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp struct {
		Results []searchResult `json:"results"`
	}
	if err := json.NewDecoder(strings.NewReader(result)).Decode(&resp); err != nil {
		t.Fatalf("failed to parse JSON: %v\ninput: %s", err, result)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one result")
	}

	r := resp.Results[0]
	// ContextBefore must be top-to-bottom: "beta" before "gamma".
	betaIdx := strings.Index(r.ContextBefore, "beta")
	gammaIdx := strings.Index(r.ContextBefore, "gamma")
	if betaIdx < 0 || gammaIdx < 0 {
		t.Errorf("expected both 'beta' and 'gamma' in context_before, got: %q", r.ContextBefore)
	} else if betaIdx > gammaIdx {
		t.Errorf("context_before order is reversed: 'beta' (idx %d) should appear before 'gamma' (idx %d), got: %q",
			betaIdx, gammaIdx, r.ContextBefore)
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
	if !strings.Contains(result, `"results":[]`) {
		t.Errorf("expected empty results array for no match, got: %s", result)
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

func TestSearchFiles_DefaultMatchCap(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// Create a file with more than searchDefaultMax matches.
	var b strings.Builder
	for range searchDefaultMax + 10 {
		b.WriteString("needle\n")
	}
	writeTestFile(t, dir, "many.txt", b.String())

	result, err := e.searchFiles(args("pattern", "needle", "format", "json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp struct {
		Results    []searchResult `json:"results"`
		Truncated  bool           `json:"truncated"`
		TotalCount int            `json:"total_count"`
	}
	dec := json.NewDecoder(strings.NewReader(result))
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("failed to parse JSON: %v\ninput: %s", err, result)
	}
	if !resp.Truncated {
		t.Errorf("expected truncated=true for >%d matches, got: %+v", searchDefaultMax, resp)
	}
	if resp.TotalCount != searchDefaultMax+10 {
		t.Errorf("expected total_count=%d, got %d", searchDefaultMax+10, resp.TotalCount)
	}
}

func TestSearchFiles_ExplicitMatchCap(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var b strings.Builder
	for range 20 {
		b.WriteString("target\n")
	}
	writeTestFile(t, dir, "cap.txt", b.String())

	result, err := e.searchFiles(args("pattern", "target", "max_matches", float64(5), "format", "json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var resp struct {
		Results    []searchResult `json:"results"`
		Truncated  bool           `json:"truncated"`
		TotalCount int            `json:"total_count"`
	}
	dec := json.NewDecoder(strings.NewReader(result))
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("failed to parse JSON: %v\ninput: %s", err, result)
	}
	if !resp.Truncated {
		t.Errorf("expected truncated=true with max_matches=5, got: %+v", resp)
	}
	if resp.TotalCount != 20 {
		t.Errorf("expected total_count=20, got %d", resp.TotalCount)
	}
}

func TestSearchFiles_TruncatedHint(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var b strings.Builder
	for range 10 {
		b.WriteString("hint\n")
	}
	writeTestFile(t, dir, "hint.txt", b.String())

	result, err := e.searchFiles(args("pattern", "hint", "max_matches", float64(3), "format", "json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "TRUNCATED") {
		t.Errorf("expected TRUNCATED hint, got: %s", result)
	}
	if !strings.Contains(result, "max_matches") {
		t.Errorf("expected 'max_matches' in hint, got: %s", result)
	}
}
