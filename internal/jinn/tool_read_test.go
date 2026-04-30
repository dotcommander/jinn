package jinn

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFile_Basic(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "test.txt", "line1\nline2\nline3\n")
	result, err := e.readFile(args("path", "test.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "1\tline1") || !strings.Contains(result.Text, "3\tline3") {
		t.Errorf("expected line-numbered output, got: %s", result.Text)
	}
}

func TestReadFile_NotFound(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.readFile(args("path", "nonexistent.txt"))
	if err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Errorf("expected 'file not found' error, got: %v", err)
	}
}

func TestReadFile_Directory(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	os.Mkdir(filepath.Join(dir, "subdir"), 0o755)
	_, err := e.readFile(args("path", "subdir"))
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("expected 'not a regular file' error, got: %v", err)
	}
}

func TestReadFile_OffsetPastEnd(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "small.txt", "one\ntwo\n")
	_, err := e.readFile(args("path", "small.txt", "start_line", float64(999)))
	if err == nil || !strings.Contains(err.Error(), "past end") {
		t.Errorf("expected 'past end' error, got: %v", err)
	}
}

func TestReadFile_BinaryDetection(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "bin.dat", "hello\x00world")
	result, err := e.readFile(args("path", "bin.dat"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "binary file") {
		t.Errorf("expected 'binary file', got: %s", result.Text)
	}
}

func TestReadFile_AlignedLineNumbers(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var content strings.Builder
	for i := range 120 {
		fmt.Fprintf(&content, "line %d\n", i+1)
	}
	writeTestFile(t, dir, "long.txt", content.String())
	result, err := e.readFile(args("path", "long.txt", "start_line", float64(1), "end_line", float64(120)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "  1\t") {
		t.Errorf("expected right-aligned line numbers, first 80 chars: %s", result.Text[:min(80, len(result.Text))])
	}
}

func TestReadFile_Window(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var content strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&content, "line%d\n", i)
	}
	writeTestFile(t, dir, "ten.txt", content.String())
	result, err := e.readFile(args("path", "ten.txt", "start_line", float64(3), "end_line", float64(5)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "line3") {
		t.Errorf("expected line3, got: %s", result.Text)
	}
	if strings.Contains(result.Text, "line2") || strings.Contains(result.Text, "line6") {
		t.Errorf("window should exclude line2 and line6, got: %s", result.Text)
	}
}

func TestReadFile_SensitivePath(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.readFile(args("path", ".git/config"))
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Errorf("expected blocked error, got: %v", err)
	}
}

func TestReadFile_Tail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		total  int
		tail   int
		want   string // must appear in output
		noWant string // must NOT appear
	}{
		{"last 3 of 10", 10, 3, "8\tline8", "line7"},
		{"tail exceeds file", 5, 100, "1\tline1", ""},
		{"tail 1", 10, 1, "10\tline10", "line9"},
		{"tail 0 disabled", 10, 0, "1\tline1", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, dir := testEngine(t)
			var b strings.Builder
			for i := 1; i <= tc.total; i++ {
				fmt.Fprintf(&b, "line%d\n", i)
			}
			writeTestFile(t, dir, "tail.txt", b.String())
			result, err := e.readFile(args("path", "tail.txt", "tail", float64(tc.tail)))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(result.Text, tc.want) {
				t.Errorf("expected %q in output, got:\n%s", tc.want, result.Text)
			}
			if tc.noWant != "" && strings.Contains(result.Text, tc.noWant) {
				t.Errorf("expected %q NOT in output, got:\n%s", tc.noWant, result.Text)
			}
		})
	}
}

func TestReadFile_TailTakesPrecedence(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	writeTestFile(t, dir, "prec.txt", b.String())
	result, err := e.readFile(args("path", "prec.txt", "tail", float64(3), "start_line", float64(1), "end_line", float64(5)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "18\tline18") {
		t.Errorf("tail should override start_line/end_line, got:\n%s", result.Text)
	}
	resultLines := strings.Split(result.Text, "\n")
	for _, l := range resultLines {
		if l == "1\tline1" || l == "5\tline5" {
			t.Errorf("start_line/end_line should be ignored when tail is set, got:\n%s", result)
			break
		}
	}
}

func TestReadFile_ContinuationHint(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var content strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&content, "line%d\n", i)
	}
	writeTestFile(t, dir, "big.txt", content.String())
	result, err := e.readFile(args("path", "big.txt", "start_line", float64(1), "end_line", float64(10)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "Remainder saved to") &&
		!strings.Contains(result.Text, "Use start_line=11 to continue") {
		t.Errorf("expected continuation hint, got: %s", result.Text)
	}
}

// --- Feature 4: error suggestions tests ---

func TestReadFile_Suggestion_Directory(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	os.Mkdir(filepath.Join(dir, "adir"), 0o755)
	_, err := e.readFile(args("path", "adir"))
	if err == nil {
		t.Fatal("expected error for directory path")
	}
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) {
		t.Fatalf("expected *ErrWithSuggestion, got %T: %v", err, err)
	}
	if !strings.Contains(sErr.Suggestion, "list_dir") {
		t.Errorf("expected 'list_dir' in suggestion, got: %s", sErr.Suggestion)
	}
}

func TestReadFile_Suggestion_NotFound(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.readFile(args("path", "no-such-file.txt"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) {
		t.Fatalf("expected *ErrWithSuggestion, got %T: %v", err, err)
	}
	if !strings.Contains(sErr.Suggestion, "list_dir") {
		t.Errorf("expected 'list_dir' in suggestion, got: %s", sErr.Suggestion)
	}
}

func TestReadFile_Suggestion_Binary(t *testing.T) {
	t.Parallel()
	// Binary detection returns a result (not an error) with a hint for the LLM.
	e, dir := testEngine(t)
	writeTestFile(t, dir, "bin.dat", "hello\x00world")
	result, err := e.readFile(args("path", "bin.dat"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "binary file") {
		t.Errorf("expected 'binary file' in result, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "checksum_tree") {
		t.Errorf("expected 'checksum_tree' hint in binary result, got: %s", result.Text)
	}
}

func TestReadFile_Suggestion_WindowOutOfRange(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "small.txt", "one\ntwo\n")
	_, err := e.readFile(args("path", "small.txt", "start_line", float64(999)))
	if err == nil {
		t.Fatal("expected error for window past end")
	}
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) {
		t.Fatalf("expected *ErrWithSuggestion, got %T: %v", err, err)
	}
	if !strings.Contains(sErr.Suggestion, "reduce start_line") {
		t.Errorf("expected 'reduce start_line' in suggestion, got: %s", sErr.Suggestion)
	}
}

func TestReadFile_Suggestion_SensitivePath(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.readFile(args("path", ".git/config"))
	if err == nil {
		t.Fatal("expected error for sensitive path")
	}
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) {
		t.Fatalf("expected *ErrWithSuggestion, got %T: %v", err, err)
	}
	if !strings.Contains(sErr.Suggestion, "blocked for security") {
		t.Errorf("expected security suggestion, got: %s", sErr.Suggestion)
	}
}

func TestReadFile_Suggestion_OutsideWorkdir(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.readFile(args("path", "/etc/passwd"))
	if err == nil {
		t.Fatal("expected error for path outside workdir")
	}
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) {
		t.Fatalf("expected *ErrWithSuggestion, got %T: %v", err, err)
	}
	if !strings.Contains(sErr.Suggestion, "sandbox root") {
		t.Errorf("expected sandbox suggestion, got: %s", sErr.Suggestion)
	}
}

func TestReadFile_LargeFile_Suggestion(t *testing.T) {
	t.Parallel()
	// Can't easily create a 50MB file in a test; test the error path via the
	// ErrWithSuggestion struct directly to verify suggestion text is correct.
	sErr := &ErrWithSuggestion{
		Err:        fmt.Errorf("file too large: 55 MB (max 50 MB)"),
		Suggestion: "file is too large to read in one shot; use start_line/end_line to window, or search_files for a pattern",
	}
	if !strings.Contains(sErr.Suggestion, "start_line/end_line") {
		t.Errorf("large file suggestion should mention start_line/end_line: %s", sErr.Suggestion)
	}
	if !strings.Contains(sErr.Suggestion, "search_files") {
		t.Errorf("large file suggestion should mention search_files: %s", sErr.Suggestion)
	}
	if sErr.Error() != sErr.Err.Error() {
		t.Errorf("ErrWithSuggestion.Error() should delegate to Err, got: %s", sErr.Error())
	}
	if sErr.Unwrap() != sErr.Err {
		t.Error("ErrWithSuggestion.Unwrap() should return Err")
	}
}

func TestReadFile_TruncationMeta_Windowed(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var content strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&content, "line%d\n", i)
	}
	writeTestFile(t, dir, "big.txt", content.String())

	result, err := e.readFile(args("path", "big.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// File has 300 lines, default window is 2000 → fits entirely → no truncation
	if result.Meta != nil {
		t.Errorf("expected nil Meta for file that fits in default window, got: %v", result.Meta)
	}
}

func TestReadFile_TruncationMeta_FitsInWindow(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "small.txt", "line1\nline2\nline3\n")

	result, err := e.readFile(args("path", "small.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3-line file fits in 2000-line window → no truncation metadata
	if result.Meta != nil {
		t.Errorf("expected nil Meta for non-truncated read, got: %v", result.Meta)
	}
}

func TestReadFile_TruncationMeta_ExplicitWindow(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var content strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&content, "line%d\n", i)
	}
	writeTestFile(t, dir, "hundred.txt", content.String())

	// Request lines 1-10 → file has 100 lines, should be truncated
	result, err := e.readFile(args("path", "hundred.txt", "start_line", float64(1), "end_line", float64(10)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Meta == nil {
		t.Fatal("expected Meta for partial window read")
	}
	trunc, ok := result.Meta["truncation"].(truncationInfo)
	if !ok {
		t.Fatalf("expected truncationInfo, got: %T", result.Meta["truncation"])
	}
	if trunc.TotalLines != 100 {
		t.Errorf("expected TotalLines=100, got: %d", trunc.TotalLines)
	}
	if trunc.OutputLines != 10 {
		t.Errorf("expected OutputLines=10, got: %d", trunc.OutputLines)
	}
	if !strings.Contains(result.Text, "Remainder saved to") {
		t.Error("expected remainder temp file hint in output")
	}
}

// --- New truncation tests for 2000-line window and 50KB byte limit ---

func TestReadFile_LargeWindow_Truncated(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var content strings.Builder
	for i := 1; i <= 2500; i++ {
		fmt.Fprintf(&content, "line%d\n", i)
	}
	writeTestFile(t, dir, "large.txt", content.String())

	result, err := e.readFile(args("path", "large.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2500 lines > 2000 default window → truncated
	if result.Meta == nil {
		t.Fatal("expected Meta for truncated read")
	}
	trunc, ok := result.Meta["truncation"].(truncationInfo)
	if !ok {
		t.Fatalf("expected truncationInfo, got: %T", result.Meta["truncation"])
	}
	if !trunc.Truncated {
		t.Error("expected Truncated=true")
	}
	if trunc.TotalLines != 2500 {
		t.Errorf("expected TotalLines=2500, got: %d", trunc.TotalLines)
	}
	if trunc.OutputLines > 2000 {
		t.Errorf("expected OutputLines <= 2000, got: %d", trunc.OutputLines)
	}
	if !strings.Contains(result.Text, "Remainder saved to") {
		t.Error("expected remainder temp file hint")
	}
}

func TestReadFile_ByteTruncation(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// Create a file where 2000 lines of ~100 chars each exceeds 50KB
	var content strings.Builder
	for i := 1; i <= 2000; i++ {
		fmt.Fprintf(&content, "line%d %s\n", i, strings.Repeat("x", 80))
	}
	writeTestFile(t, dir, "wide.txt", content.String())

	result, err := e.readFile(args("path", "wide.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Meta == nil {
		t.Fatal("expected Meta for byte-truncated read")
	}
	trunc, ok := result.Meta["truncation"].(truncationInfo)
	if !ok {
		t.Fatalf("expected truncationInfo, got: %T", result.Meta["truncation"])
	}
	if !trunc.Truncated {
		t.Error("expected Truncated=true")
	}
	if trunc.TotalLines != 2000 {
		t.Errorf("expected TotalLines=2000, got: %d", trunc.TotalLines)
	}
	// Should have stopped well short of 2000 due to byte limit
	if trunc.OutputLines >= 2000 {
		t.Errorf("expected OutputLines < 2000 due to byte truncation, got: %d", trunc.OutputLines)
	}
	if !strings.Contains(result.Text, "truncated at") {
		t.Error("expected 'truncated at' in byte truncation hint")
	}
}

func TestReadFile_RawOutput_NoLineNumbers(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "raw.txt", "alpha\nbeta\ngamma\ndelta\nepsilon\n")
	result, err := e.readFile(args("path", "raw.txt", "line_numbers", false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Text, "\t") {
		t.Errorf("expected no tab separator in raw output, got: %s", result.Text)
	}
	for _, want := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("expected %q in raw output, got: %s", want, result.Text)
		}
	}
}

func TestReadFile_RawOutput_RespectsWindow(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	writeTestFile(t, dir, "windowed.txt", b.String())
	result, err := e.readFile(args("path", "windowed.txt", "line_numbers", false, "start_line", float64(3), "end_line", float64(5)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Text, "\t") {
		t.Errorf("expected no tab separator in raw windowed output, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "line3") || !strings.Contains(result.Text, "line5") {
		t.Errorf("expected lines 3-5 in output, got: %s", result.Text)
	}
	if strings.Contains(result.Text, "line2") || strings.Contains(result.Text, "line6") {
		t.Errorf("window should exclude line2 and line6, got: %s", result.Text)
	}
}

func TestReadFile_DefaultIncludesLineNumbers(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "default.txt", "foo\nbar\nbaz\n")
	result, err := e.readFile(args("path", "default.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "1\t") {
		t.Errorf("expected cat-n line numbers by default, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "2\t") || !strings.Contains(result.Text, "3\t") {
		t.Errorf("expected all line numbers, got: %s", result.Text)
	}
}

func TestReadFile_FitsInDefaultWindow_NoTruncation(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var content strings.Builder
	for i := 1; i <= 1999; i++ {
		fmt.Fprintf(&content, "line%d\n", i)
	}
	writeTestFile(t, dir, "exact.txt", content.String())

	result, err := e.readFile(args("path", "exact.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1999 lines fits in 2000-line default window → no truncation
	if result.Meta != nil {
		t.Errorf("expected nil Meta for non-truncated read, got: %v", result.Meta)
	}
}
