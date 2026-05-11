package jinn

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// estimateTokens returns a rough token count for a string.
// Uses ~4 chars/token for plain text, ~3.5 chars/token for JSON/code.
// Good enough for efficiency assertions — not exact tiktoken.
func estimateTokens(s string) float64 {
	if utf8.RuneCountInString(s) == 0 {
		return 0
	}
	jsonDensity := float64(strings.Count(s, "{") + strings.Count(s, "}") + strings.Count(s, `"`) + strings.Count(s, ":"))
	charRatio := 4.0
	if jsonDensity > float64(utf8.RuneCountInString(s))*0.02 {
		charRatio = 3.5
	}
	return float64(utf8.RuneCountInString(s)) / charRatio
}

// tokenBudget tracks token economics for a test case.
type tokenBudget struct {
	InputTokens  float64
	OutputTokens float64
	RawTokens    float64 // uncompressed size (for compression ratio)
}

// ratio returns output/raw ratio (lower is better compression).
func (tb tokenBudget) ratio() float64 {
	if tb.RawTokens == 0 {
		return 1.0
	}
	return tb.OutputTokens / tb.RawTokens
}

// overheadRatio returns the fraction of output lines that are structural
// metadata (lines starting with [, empty lines, pure braces, truncation keys).
func overheadRatio(output string) float64 {
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return 0
	}
	overhead := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") || trimmed == "" || trimmed == "{" || trimmed == "}" || strings.HasPrefix(trimmed, `"truncation"`) {
			overhead++
		}
	}
	return float64(overhead) / float64(len(lines))
}

// ---------------------------------------------------------------------------
// Group 1: High-compression tools
// ---------------------------------------------------------------------------

func TestTokenEfficiency_RunShell_CollapseRepetition(t *testing.T) {
	t.Parallel()
	// 1000 identical lines → should collapse to ~3 lines + annotation
	var b strings.Builder
	for range 1000 {
		b.WriteString("identical output line\n")
	}
	raw := b.String()
	rawTokens := estimateTokens(raw)

	compressed := collapseRepeatedLines(raw)
	compressedTokens := estimateTokens(compressed)
	ratio := compressedTokens / rawTokens

	if ratio > 0.1 {
		t.Errorf("collapseRepeatedLines only achieved %.1f%% compression (ratio %.2f), expected >90%%", (1-ratio)*100, ratio)
	}
	if !strings.Contains(compressed, "identical lines collapsed") {
		t.Error("expected collapse annotation")
	}
}

func TestTokenEfficiency_RunShell_BlankCollapse(t *testing.T) {
	t.Parallel()
	// 500 blank lines → collapse to threshold
	var b strings.Builder
	b.WriteString("start\n")
	for range 500 {
		b.WriteString("\n")
	}
	b.WriteString("end\n")

	raw := b.String()
	rawTokens := estimateTokens(raw)

	compressed := collapseBlankLines(raw, 3)
	compressedTokens := estimateTokens(compressed)
	ratio := compressedTokens / rawTokens

	if ratio > 0.05 {
		t.Errorf("collapseBlankLines only achieved %.1f%% compression (ratio %.2f), expected >95%%", (1-ratio)*100, ratio)
	}
	if !strings.Contains(compressed, "start") || !strings.Contains(compressed, "end") {
		t.Error("expected non-blank content preserved")
	}
}

func TestTokenEfficiency_RunShell_TailTruncation(t *testing.T) {
	t.Parallel()
	// 5000 lines of shell output → tail keeps last 2000
	var b strings.Builder
	for i := range 5000 {
		fmt.Fprintf(&b, "output line %d with some content\n", i)
	}
	raw := b.String()
	rawTokens := estimateTokens(raw)

	content, _ := truncateTailDetailed(raw, DefaultMaxLines, DefaultMaxBytes)
	compressedTokens := estimateTokens(content)
	ratio := compressedTokens / rawTokens

	if ratio > 0.45 {
		t.Errorf("tail truncation ratio %.2f, expected <0.45", ratio)
	}
	// Should contain the last line
	if !strings.Contains(content, "output line 4999") {
		t.Error("expected last line preserved in tail truncation")
	}
}

func TestTokenEfficiency_ReadFile_HeadTruncation(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	var b strings.Builder
	for i := 1; i <= 5000; i++ {
		fmt.Fprintf(&b, "line %d content here\n", i)
	}
	writeTestFile(t, dir, "big.txt", b.String())

	result, err := e.readFile(args("path", "big.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	outputTokens := estimateTokens(result.Text)
	rawTokens := estimateTokens(b.String())
	ratio := outputTokens / rawTokens

	if ratio > 0.5 {
		t.Errorf("read_file head truncation ratio %.2f, expected <0.5", ratio)
	}
	if !strings.Contains(result.Text, "1\tline 1") {
		t.Error("expected first line preserved")
	}
}

func TestTokenEfficiency_ReadFile_SmartTruncation(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Build a file with many Go functions — smart truncation should keep
	// complete functions at brace boundaries.
	var b strings.Builder
	for i := range 800 {
		fmt.Fprintf(&b, "func Func%d() int {\n", i)
		b.WriteString("\t// function body with some padding to make lines longer\n")
		b.WriteString("\tx := compute(i)\n")
		b.WriteString("\ty := transform(x)\n")
		b.WriteString("\treturn y\n")
		b.WriteString("}\n\n")
	}
	writeTestFile(t, dir, "funcs.go", b.String())

	result, err := e.readFile(args("path", "funcs.go", "truncate", "smart"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	outputTokens := estimateTokens(result.Text)
	rawTokens := estimateTokens(b.String())
	ratio := outputTokens / rawTokens

	if ratio > 0.7 {
		t.Errorf("smart truncation ratio %.2f, expected <0.7", ratio)
	}
}

func TestTokenEfficiency_ReadFile_ByteCap(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Build a file that exceeds the 50KB byte cap: each line ~100 bytes, 600 lines.
	var b strings.Builder
	for i := range 600 {
		fmt.Fprintf(&b, "%s line content padding to make it longer than usual %d\n",
			strings.Repeat("x", 70), i)
	}
	writeTestFile(t, dir, "bigbytes.txt", b.String())

	result, err := e.readFile(args("path", "bigbytes.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Text) > readMaxBytes+2000 { // allow room for hint text
		t.Errorf("output %d bytes exceeds 50KB cap + hint margin", len(result.Text))
	}
}

func TestTokenEfficiency_MultiRead_PerFileCompression(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Create 3 files: 2 large Go files that get truncated, 1 small
	var large1, large2 strings.Builder
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&large1, "package main\n// content line %d\n", i)
	}
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&large2, "package main\n// different line %d\n", i)
	}
	writeTestFile(t, dir, "large1.go", large1.String())
	writeTestFile(t, dir, "large2.go", large2.String())
	writeTestFile(t, dir, "small.go", "package main\n")

	files := []any{
		map[string]any{"path": "large1.go"},
		map[string]any{"path": "large2.go"},
		map[string]any{"path": "small.go"},
	}
	result, err := e.multiRead(args("files", files))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parse result JSON to verify per-file truncation metadata
	var mr multiReadResult
	if err := json.Unmarshal([]byte(result.Text), &mr); err != nil {
		t.Fatalf("unmarshal multi_read result: %v", err)
	}

	// Both large files should have truncation info
	if len(mr.Truncation) < 2 {
		t.Errorf("expected truncation info for 2 large files, got %d", len(mr.Truncation))
	}
	for _, path := range []string{"large1.go", "large2.go"} {
		ti, ok := mr.Truncation[path]
		if !ok {
			t.Errorf("missing truncation info for %s", path)
			continue
		}
		if !ti.Truncated {
			t.Errorf("expected %s to be truncated", path)
		}
		if ti.OutputLines >= ti.TotalLines {
			t.Errorf("expected output < total for %s: output=%d total=%d", path, ti.OutputLines, ti.TotalLines)
		}
	}

	// Small file should NOT be truncated
	if _, ok := mr.Truncation["small.go"]; ok {
		t.Error("small.go should not be truncated")
	}

	// Verify the truncation metadata is concise (< 200 tokens total)
	var metaTokens float64
	metaJSON, _ := json.Marshal(mr.Truncation)
	metaTokens = estimateTokens(string(metaJSON))
	if metaTokens > 200 {
		t.Errorf("truncation metadata is %d tokens, expected < 200", int(metaTokens))
	}
}

func TestTokenEfficiency_SearchFiles_MaxMatches(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Create a file with 600 lines that all match the pattern
	var b strings.Builder
	for i := range 600 {
		fmt.Fprintf(&b, "line %d has MARKER_PATTERN_HERE\n", i)
	}
	writeTestFile(t, dir, "big.go", b.String())

	result, err := e.searchFiles(args("pattern", "MARKER_PATTERN_HERE", "format", "json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "TRUNCATED") {
		t.Errorf("expected TRUNCATED hint for 600 matches, got prefix: %s", result[:min(200, len(result))])
	}

	// Verify truncated=true in JSON
	dec := json.NewDecoder(strings.NewReader(result))
	var resp struct {
		Truncated  bool `json:"truncated"`
		TotalCount int  `json:"total_count"`
	}
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("unmarshal search result: %v", err)
	}
	if !resp.Truncated {
		t.Error("expected truncated=true")
	}
	if resp.TotalCount <= searchDefaultMax {
		t.Errorf("expected total_count > %d, got %d", searchDefaultMax, resp.TotalCount)
	}
}

// ---------------------------------------------------------------------------
// Group 2: Entry-capped tools
// ---------------------------------------------------------------------------

func TestTokenEfficiency_ListDir_EntryCap(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Create 600 files to exceed the default 500 cap
	for i := range 600 {
		writeTestFile(t, dir, fmt.Sprintf("file%04d.txt", i), "")
	}

	result, err := e.listDir(args("depth", float64(1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "TRUNCATED") {
		t.Errorf("expected TRUNCATED hint, got prefix: %s", result[:min(200, len(result))])
	}

	// Extract hint and verify it's concise
	hintStart := strings.Index(result, "[TRUNCATED")
	if hintStart < 0 {
		t.Fatal("TRUNCATED hint not found")
	}
	hint := result[hintStart:]
	// Trim any trailing newline
	hint = strings.TrimSpace(hint)
	if len(hint) > 100 {
		t.Errorf("truncation hint is %d chars, expected < 100: %q", len(hint), hint)
	}
}

func TestTokenEfficiency_FindFiles_Limit(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Create many .txt files to exceed the default 1000 limit
	for i := range 1100 {
		writeTestFile(t, dir, fmt.Sprintf("doc%04d.txt", i), "")
	}

	result, err := e.findFiles(args("pattern", "*.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := parseFindResult(t, result)
	if !res.Truncated {
		t.Error("expected truncated=true")
	}
	if res.TotalCount <= findDefaultLimit {
		t.Errorf("expected total_count > %d, got %d", findDefaultLimit, res.TotalCount)
	}
	if len(res.Files) > findDefaultLimit {
		t.Errorf("returned %d files, expected capped at %d", len(res.Files), findDefaultLimit)
	}

	// Verify hint conciseness
	if strings.Contains(result, "[TRUNCATED") {
		hintStart := strings.Index(result, "[TRUNCATED")
		hint := strings.TrimSpace(result[hintStart:])
		if len(hint) > 100 {
			t.Errorf("truncation hint is %d chars, expected < 100: %q", len(hint), hint)
		}
	}
}

func TestTokenEfficiency_EditFile_AmbiguousCap(t *testing.T) {
	t.Parallel()
	// 20 occurrences → multiMatchError reports max 10 line numbers
	var b strings.Builder
	for range 20 {
		b.WriteString("target line to match\n")
	}
	needle := "target line to match"
	haystack := b.String()

	err := multiMatchError(20, haystack, needle)
	if err == nil {
		t.Fatal("expected error for 20 matches")
	}

	msg := err.Error()
	if !strings.Contains(msg, "matches 20 locations") {
		t.Errorf("expected match count in error, got: %s", msg)
	}
	if !strings.Contains(msg, "... and 10 more") {
		t.Errorf("expected '... and 10 more' (cap at 10 line numbers), got: %s", msg)
	}

	// Verify at most 10 line numbers are listed (not 20)
	lines, _ := collectMatchLines(haystack, needle, 10)
	if len(lines) != 10 {
		t.Errorf("expected exactly 10 line numbers reported, got %d", len(lines))
	}
}

// ---------------------------------------------------------------------------
// Group 3: Tools without compression (verify minimal output)
// ---------------------------------------------------------------------------

func TestTokenEfficiency_StatFile_OutputSize(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "test.txt", "hello world\n")

	result, err := e.statFile(args("path", "test.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tokens := estimateTokens(result)
	if tokens > 500 {
		t.Errorf("stat_file output is %.0f tokens, expected < 500: %s", tokens, result)
	}
}

func TestTokenEfficiency_WriteFile_OutputSize(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	result, err := e.writeFile(args("path", "output.txt", "content", "some content"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tokens := estimateTokens(result)
	if tokens > 50 {
		t.Errorf("write_file output is %.0f tokens, expected < 50: %s", tokens, result)
	}
}

func TestTokenEfficiency_ListTools_OutputSize(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	result, _, err := e.Dispatch(nil, "list_tools", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tokens := estimateTokens(result.Text)
	if tokens > 6000 {
		t.Errorf("list_tools output is %.0f tokens, expected < 6000", tokens)
	}
}

func TestTokenEfficiency_Memory_OutputSize(t *testing.T) {
	// NOTE: Cannot use t.Parallel() — t.Setenv is serial only.
	memCfgDir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", memCfgDir)
	e, _ := testEngine(t)

	// Save
	saveOut, err := e.memoryTool(args("action", "save", "key", "test-key", "value", "test-value"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if tokens := estimateTokens(saveOut); tokens > 100 {
		t.Errorf("memory save output is %.0f tokens, expected < 100: %s", tokens, saveOut)
	}

	// Recall
	recallOut, err := e.memoryTool(args("action", "recall", "key", "test-key"))
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if tokens := estimateTokens(recallOut); tokens > 100 {
		t.Errorf("memory recall output is %.0f tokens, expected < 100: %s", tokens, recallOut)
	}

	// List
	listOut, err := e.memoryTool(args("action", "list"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if tokens := estimateTokens(listOut); tokens > 100 {
		t.Errorf("memory list output is %.0f tokens, expected < 100: %s", tokens, listOut)
	}
}

func TestTokenEfficiency_DetectProject_OutputSize(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "go.mod", "module example.com/test\ngo 1.26\n")

	result, err := e.detectProject(args("path", "."))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tokens := estimateTokens(result)
	if tokens > 200 {
		t.Errorf("detect_project output is %.0f tokens, expected < 200: %s", tokens, result)
	}
}

func TestTokenEfficiency_Undo_OutputSize(t *testing.T) {
	// NOTE: Cannot use t.Parallel() — t.Setenv is serial only.
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e, dir := testEngine(t)

	// Create a file and write to it to generate undo history
	writeTestFile(t, dir, "undo_test.txt", "original content\n")
	if _, err := e.writeFile(args("path", "undo_test.txt", "content", "updated content\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	result, err := e.undoTool(args("action", "list"))
	if err != nil {
		t.Fatalf("undo list: %v", err)
	}

	tokens := estimateTokens(result)
	if tokens > 500 {
		t.Errorf("undo list output is %.0f tokens, expected < 500", tokens)
	}
}

// ---------------------------------------------------------------------------
// Group 4: Boundary tests
// ---------------------------------------------------------------------------

func TestTokenEfficiency_ReadFile_ExactLimitBoundary(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// File with exactly 2000 lines → still within window (1-2000), but
	// truncateOutputHead triggers at count >= limit, so 2000 lines
	// at the default limit ARE truncated by the head strategy.
	// This test verifies the boundary behavior is well-defined.
	var b strings.Builder
	for i := 1; i <= readDefaultLines; i++ {
		fmt.Fprintf(&b, "boundary line %d\n", i)
	}
	writeTestFile(t, dir, "exact.txt", b.String())

	result, err := e.readFile(args("path", "exact.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// truncateOutputHead uses count >= limit → 2000 lines triggers truncation.
	// This is the defined boundary behavior.
	if result.Meta != nil {
		if trunc, ok := result.Meta["truncation"]; ok {
			ti := trunc.(truncationInfo)
			// Verify that exactly 2000 lines triggers truncation
			if !ti.Truncated {
				t.Error("expected truncated=true at exactly 2000 output lines")
			}
		}
	}
}

func TestTokenEfficiency_ReadFile_OneOverLimit(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// File with 2001 lines → truncated
	var b strings.Builder
	for i := 1; i <= readDefaultLines+1; i++ {
		fmt.Fprintf(&b, "over line %d\n", i)
	}
	writeTestFile(t, dir, "over.txt", b.String())

	result, err := e.readFile(args("path", "over.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be truncated
	if result.Meta == nil {
		t.Fatal("expected truncation metadata")
	}
	trunc, ok := result.Meta["truncation"]
	if !ok {
		t.Fatal("expected truncation key in meta")
	}
	ti, ok := trunc.(truncationInfo)
	if !ok {
		t.Fatalf("expected truncationInfo, got %T", trunc)
	}
	if !ti.Truncated {
		t.Error("expected truncated=true for 2001 lines")
	}

	rawTokens := estimateTokens(b.String())
	outputTokens := estimateTokens(result.Text)
	ratio := outputTokens / rawTokens
	// With line numbers and continuation hint, the output can be slightly larger
	// than the raw source. Verify truncation actually happened.
	if !ti.Truncated {
		t.Error("expected truncated=true for 2001 lines")
	}
	// The output should be bounded: no more than 1.5x the line content
	// (line numbers add overhead but the file is one line over the limit)
	if ratio > 1.5 {
		t.Errorf("one-over truncation ratio %.3f, expected <= 1.5", ratio)
	}
}

func TestTokenEfficiency_SearchFiles_ExactMatchBoundary(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Create a file with exactly 500 matching lines
	var b strings.Builder
	for i := range searchDefaultMax {
		fmt.Fprintf(&b, "EXACT_BOUNDARY_MATCH_%d\n", i)
	}
	writeTestFile(t, dir, "exact_match.go", b.String())

	result, err := e.searchFiles(args("pattern", "EXACT_BOUNDARY_MATCH", "format", "json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dec := json.NewDecoder(strings.NewReader(result))
	var resp struct {
		Truncated  bool `json:"truncated"`
		TotalCount int  `json:"total_count"`
	}
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Exactly 500 matches should NOT be truncated (cap is exclusive: >500 triggers)
	if resp.TotalCount != searchDefaultMax {
		t.Errorf("expected total_count=%d, got %d", searchDefaultMax, resp.TotalCount)
	}
	if resp.Truncated {
		t.Error("exactly 500 matches should not be truncated")
	}
	if strings.Contains(result, "TRUNCATED") {
		t.Error("should not have TRUNCATED hint at exactly 500 matches")
	}
}

func TestTokenEfficiency_SearchFiles_OneOverMatchBoundary(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Create a file with 501 matching lines
	var b strings.Builder
	for i := range searchDefaultMax + 1 {
		fmt.Fprintf(&b, "OVER_BOUNDARY_MATCH_%d\n", i)
	}
	writeTestFile(t, dir, "over_match.go", b.String())

	result, err := e.searchFiles(args("pattern", "OVER_BOUNDARY_MATCH", "format", "json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	dec := json.NewDecoder(strings.NewReader(result))
	var resp struct {
		Truncated  bool `json:"truncated"`
		TotalCount int  `json:"total_count"`
	}
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.TotalCount <= searchDefaultMax {
		t.Errorf("expected total_count > %d, got %d", searchDefaultMax, resp.TotalCount)
	}
	if !resp.Truncated {
		t.Error("501 matches should be truncated")
	}
	if !strings.Contains(result, "TRUNCATED") {
		t.Error("expected TRUNCATED hint for 501 matches")
	}
}

// ---------------------------------------------------------------------------
// Group 5: Noise ratio tests
// ---------------------------------------------------------------------------

func TestTokenEfficiency_TruncationHint_Conciseness(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Table of test cases that produce truncated output.
	tests := []struct {
		name string
		setup func() string // returns truncated output text
	}{
		{
			name: "read_file large",
			setup: func() string {
				var b strings.Builder
				for i := 1; i <= 5000; i++ {
					fmt.Fprintf(&b, "data line %d with content\n", i)
				}
				writeTestFile(t, dir, "hint_test.txt", b.String())
				result, err := e.readFile(args("path", "hint_test.txt"))
				if err != nil {
					t.Fatalf("readFile: %v", err)
				}
				return result.Text
			},
		},
		{
			name: "list_dir many entries",
			setup: func() string {
				for i := range 600 {
					writeTestFile(t, dir, fmt.Sprintf("hf%04d.txt", i), "")
				}
				result, err := e.listDir(args("depth", float64(1)))
				if err != nil {
					t.Fatalf("listDir: %v", err)
				}
				return result
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			output := tc.setup()

			// Find any truncation hints ([...] markers)
			totalTokens := estimateTokens(output)
			if totalTokens == 0 {
				t.Fatal("output is empty")
			}

			var hintTokens float64
			lines := strings.Split(output, "\n")
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "[truncated") ||
					strings.HasPrefix(trimmed, "[TRUNCATED") ||
					strings.HasPrefix(trimmed, "[Showing") {
					hintTokens += estimateTokens(trimmed)
				}
			}

			if hintTokens > 0 && totalTokens > 0 {
				ratio := hintTokens / totalTokens
				if ratio > 0.05 {
					t.Errorf("hint tokens %.0f / total %.0f = %.1f%%, expected < 5%%",
						hintTokens, totalTokens, ratio*100)
				}
			}
		})
	}
}

func TestTokenEfficiency_ReadFile_NoRedundantMetadata(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Create a file large enough to trigger both line truncation and byte hints
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&b, "metadata test line %d with padding content here %s\n", i, strings.Repeat("x", 20))
	}
	writeTestFile(t, dir, "meta_test.txt", b.String())

	result, err := e.readFile(args("path", "meta_test.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the text contains a continuation hint but not duplicate info
	hintCount := strings.Count(result.Text, "start_line=")
	if hintCount > 1 {
		t.Errorf("found %d start_line= hints (expected 1), possible duplication:\n%s",
			hintCount, result.Text[max(0, len(result.Text)-500):])
	}

	// ByteHint and truncation meta should not both claim different line counts
	if result.Meta != nil {
		if trunc, ok := result.Meta["truncation"]; ok {
			ti, ok := trunc.(truncationInfo)
			if !ok {
				t.Fatalf("expected truncationInfo, got %T", trunc)
			}
			// If there's a ByteHint in the text, the total_lines should be consistent
			if strings.Contains(result.Text, "Showing lines") {
				// The truncation metadata and text hint should agree on total
				if ti.TotalLines == 0 {
					t.Error("truncation metadata has TotalLines=0 but text shows continuation hint")
				}
			}
			// Overhead ratio: structural metadata should be small
			ohRatio := overheadRatio(result.Text)
			if ohRatio > 0.1 {
				t.Errorf("overhead ratio %.1f%%, expected < 10%%", ohRatio*100)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Additional coverage: compression pipeline integration
// ---------------------------------------------------------------------------

func TestTokenEfficiency_RunShell_FullCompressionPipeline(t *testing.T) {
	t.Parallel()
	// Simulate the full run_shell compression pipeline:
	// 1. collapseRepeatedLines
	// 2. collapseBlankLines
	// 3. truncateTailDetailed
	var b strings.Builder
	// 200 repeated lines
	for range 200 {
		b.WriteString("same same same\n")
	}
	// 100 blank lines
	for range 100 {
		b.WriteString("\n")
	}
	// 3000 unique lines
	for i := range 3000 {
		fmt.Fprintf(&b, "unique output %d\n", i)
	}

	raw := b.String()
	rawTokens := estimateTokens(raw)

	// Apply the compression pipeline
	compressed := collapseRepeatedLines(raw)
	compressed = collapseBlankLines(compressed, 3)
	content, _ := truncateTailDetailed(compressed, DefaultMaxLines, DefaultMaxBytes)

	outputTokens := estimateTokens(content)
	ratio := outputTokens / rawTokens

	if ratio > 0.7 {
		t.Errorf("full pipeline ratio %.2f, expected < 0.7", ratio)
	}

	// Verify structural overhead is low
	ohRatio := overheadRatio(content)
	if ohRatio > 0.05 {
		t.Errorf("overhead ratio %.1f%%, expected < 5%%", ohRatio*100)
	}
}

func TestTokenEfficiency_RunShell_NoCompressionNeeded(t *testing.T) {
	t.Parallel()
	// Short, unique output should pass through unchanged
	input := "line 1\nline 2\nline 3\n"
	rawTokens := estimateTokens(input)

	compressed := collapseRepeatedLines(input)
	compressed = collapseBlankLines(compressed, 3)

	outputTokens := estimateTokens(compressed)
	// Ratio should be ~1.0 (no compression needed)
	ratio := math.Abs(outputTokens-rawTokens) / rawTokens
	if ratio > 0.05 {
		t.Errorf("no-compression case changed output by %.1f%%, expected < 5%%", ratio*100)
	}
}

// helper: readDefaultLines boundary validation via direct function calls
func TestTokenEfficiency_TruncateHead_BoundaryBehavior(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		lineCount int
		wantTrunc bool
	}{
		{"at limit", readDefaultLines, true}, // truncateOutputHead uses count >= limit
		{"one under", readDefaultLines - 1, false},
		{"one over", readDefaultLines + 1, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var b strings.Builder
			for i := range tc.lineCount {
				fmt.Fprintf(&b, "line %d\n", i)
			}
			result := truncateOutputHead(b.String(), readDefaultLines)
			if result.Truncated != tc.wantTrunc {
				t.Errorf("lineCount=%d: truncated=%v, want %v", tc.lineCount, result.Truncated, tc.wantTrunc)
			}
		})
	}
}

// Ensure file creation works for many-file tests without blowing the fs
func TestTokenEfficiency_FindFiles_ExactLimitBoundary(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Create exactly findDefaultLimit files
	for i := range findDefaultLimit {
		writeTestFile(t, dir, fmt.Sprintf("lim%04d.txt", i), "")
	}

	result, err := e.findFiles(args("pattern", "*.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := parseFindResult(t, result)
	if res.Truncated {
		t.Errorf("exactly %d files should not be truncated", findDefaultLimit)
	}
	if res.TotalCount != findDefaultLimit {
		t.Errorf("expected total_count=%d, got %d", findDefaultLimit, res.TotalCount)
	}
}

// Helper to create many files in a subdirectory (used by search boundary tests)
func createManyFiles(t *testing.T, dir, prefix string, count int, contentLine string) {
	t.Helper()
	for i := range count {
		name := fmt.Sprintf("%s%04d.txt", prefix, i)
		writeTestFile(t, dir, name, contentLine+"\n")
	}
}

// Verify that the output from read_file with line_numbers=false is smaller
func TestTokenEfficiency_ReadFile_LineNumbersOff(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	var b strings.Builder
	for i := 1; i <= 500; i++ {
		fmt.Fprintf(&b, "content line %04d here\n", i)
	}
	writeTestFile(t, dir, "numoff.txt", b.String())

	withNums, err := e.readFile(args("path", "numoff.txt", "line_numbers", true))
	if err != nil {
		t.Fatalf("with line_numbers: %v", err)
	}
	withoutNums, err := e.readFile(args("path", "numoff.txt", "line_numbers", false))
	if err != nil {
		t.Fatalf("without line_numbers: %v", err)
	}

	withTokens := estimateTokens(withNums.Text)
	withoutTokens := estimateTokens(withoutNums.Text)
	if withoutTokens >= withTokens {
		t.Errorf("line_numbers=false (%.0f tokens) should be smaller than true (%.0f tokens)",
			withoutTokens, withTokens)
	}
}

// Verify checksum metadata doesn't bloat output
func TestTokenEfficiency_StatFile_NoBloat(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Create a real file to get realistic stat output
	writeTestFile(t, dir, "statme.txt", "content\n")

	result, err := e.statFile(args("path", "statme.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Stat output should not contain redundant info
	lines := strings.Split(result, "\n")
	nonEmpty := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			nonEmpty++
		}
	}
	if nonEmpty > 10 {
		t.Errorf("stat output has %d non-empty lines, expected <= 10:\n%s", nonEmpty, result)
	}
}

// Verify undo list with many entries stays bounded
func TestTokenEfficiency_UndoList_ManyEntries(t *testing.T) {
	// NOTE: Cannot use t.Parallel() — t.Setenv is serial only.
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e, dir := testEngine(t)

	// Create 20 undo entries
	for i := range 20 {
		name := fmt.Sprintf("undo_%d.txt", i)
		writeTestFile(t, dir, name, fmt.Sprintf("content %d\n", i))
		if _, err := e.writeFile(args("path", name, "content", fmt.Sprintf("updated %d\n", i))); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	// List with limit
	result, err := e.undoTool(args("action", "list", "limit", float64(5)))
	if err != nil {
		t.Fatalf("undo list: %v", err)
	}

	tokens := estimateTokens(result)
	if tokens > 1000 {
		t.Errorf("undo list with limit=5 is %.0f tokens, expected < 1000", tokens)
	}

	// Verify the limit was respected
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if count := int(out["count"].(float64)); count > 5 {
		t.Errorf("expected count <= 5, got %d", count)
	}
}

// Ensure search_files filenames format is compact
func TestTokenEfficiency_SearchFiles_FilenamesFormat(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Create files with matching content
	for i := range 10 {
		writeTestFile(t, dir, fmt.Sprintf("sf%02d.go", i), "package main // UNIQUE_PATTERN\n")
	}

	result, err := e.searchFiles(args("pattern", "UNIQUE_PATTERN", "format", "filenames"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Filenames format should be compact: just file:count pairs
	tokens := estimateTokens(result)
	if tokens > 200 {
		t.Errorf("filenames format is %.0f tokens for 10 files, expected < 200: %s", tokens, result)
	}
}

// Verify detect_project with no config files is still concise
func TestTokenEfficiency_DetectProject_Empty(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	// Empty directory — detect should still return concise JSON
	result, err := e.detectProject(args("path", "."))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tokens := estimateTokens(result)
	if tokens > 100 {
		t.Errorf("detect_project on empty dir is %.0f tokens, expected < 100: %s", tokens, result)
	}

	// Should be valid JSON
	var info projectInfo
	if err := json.Unmarshal([]byte(result), &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

// Verify find_files subdirectory search doesn't leak excessive paths
func TestTokenEfficiency_FindFiles_SubdirectoryPaths(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Create nested files
	for i := range 5 {
		sub := fmt.Sprintf("pkg%d", i)
		os.MkdirAll(filepath.Join(dir, sub), 0o755)
		writeTestFile(t, dir, fmt.Sprintf("%s/main.go", sub), "package main\n")
	}

	result, err := e.findFiles(args("pattern", "*.go"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := parseFindResult(t, result)
	if len(res.Files) != 5 {
		t.Errorf("expected 5 files, got %d: %v", len(res.Files), res.Files)
	}

	// Verify paths are relative and not absolute
	for _, f := range res.Files {
		if filepath.IsAbs(f) {
			t.Errorf("path should be relative, got absolute: %s", f)
		}
	}
}
