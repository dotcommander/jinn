package jinn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchReplace_BasicSingleFile(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "hello world\nhello universe\n")

	result, err := e.searchReplace(context.Background(), args(
		"pattern", "hello",
		"replacement", "hi",
		"files", "a.txt",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "2 replacements") {
		t.Errorf("expected 2 replacements, got: %s", result.Text)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	got := string(data)
	if strings.Contains(got, "hello") {
		t.Errorf("file should have no 'hello' remaining, got: %s", got)
	}
	if !strings.Contains(got, "hi world") || !strings.Contains(got, "hi universe") {
		t.Errorf("expected 'hi world' and 'hi universe', got: %s", got)
	}
}

func TestSearchReplace_CaptureGroups(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "code.go", "fmt.Sprintf(\"%d\", x)\nfmt.Sprintf(\"%s\", y)\n")

	result, err := e.searchReplace(context.Background(), args(
		"pattern", `fmt\.Sprintf\("(.+?)",\s*(\w+)\)`,
		"replacement", `format($2, $1)`,
		"files", "code.go",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "2 replacements") {
		t.Errorf("expected 2 replacements, got: %s", result.Text)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "code.go"))
	got := string(data)
	if !strings.Contains(got, "format(x, %d)") || !strings.Contains(got, "format(y, %s)") {
		t.Errorf("expected capture group replacement, got: %s", got)
	}
}

func TestSearchReplace_CaseInsensitive(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "mixed.txt", "Hello HELLO hello\n")

	result, err := e.searchReplace(context.Background(), args(
		"pattern", "hello",
		"replacement", "hi",
		"files", "mixed.txt",
		"case_insensitive", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "3 replacements") {
		t.Errorf("expected 3 replacements, got: %s", result.Text)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "mixed.txt"))
	got := string(data)
	if got != "hi hi hi\n" {
		t.Errorf("expected all 'hi', got: %s", got)
	}
}

func TestSearchReplace_DryRun(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "dry.txt", "foo bar baz\n")

	result, err := e.searchReplace(context.Background(), args(
		"pattern", "bar",
		"replacement", "qux",
		"files", "dry.txt",
		"dry_run", true,
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "[dry-run]") {
		t.Errorf("expected dry-run prefix, got: %s", result.Text)
	}

	// File should be unchanged.
	data, _ := os.ReadFile(filepath.Join(dir, "dry.txt"))
	got := string(data)
	if !strings.Contains(got, "bar") {
		t.Errorf("file should be unchanged after dry_run, got: %s", got)
	}
}

func TestSearchReplace_MultiFile(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "old value\n")
	writeTestFile(t, dir, "b.txt", "old value\n")
	writeTestFile(t, dir, "c.txt", "no match here\n")

	result, err := e.searchReplace(context.Background(), args(
		"pattern", "old",
		"replacement", "new",
		"files", []interface{}{"a.txt", "b.txt", "c.txt"},
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "2 files") {
		t.Errorf("expected 2 files changed, got: %s", result.Text)
	}

	dataA, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	dataB, _ := os.ReadFile(filepath.Join(dir, "b.txt"))
	dataC, _ := os.ReadFile(filepath.Join(dir, "c.txt"))
	if string(dataA) != "new value\n" {
		t.Errorf("a.txt: expected 'new value', got: %s", dataA)
	}
	if string(dataB) != "new value\n" {
		t.Errorf("b.txt: expected 'new value', got: %s", dataB)
	}
	if string(dataC) != "no match here\n" {
		t.Errorf("c.txt: should be unchanged, got: %s", dataC)
	}
}

func TestSearchReplace_Deletion(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "del.txt", "keep\nremove_me\nkeep\n")

	_, err := e.searchReplace(context.Background(), args(
		"pattern", "remove_me\n",
		"replacement", "",
		"files", "del.txt",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "del.txt"))
	if string(data) != "keep\nkeep\n" {
		t.Errorf("expected deletion, got: %s", data)
	}
}

func TestSearchReplace_NoMatch(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "nomatch.txt", "nothing to see\n")

	result, err := e.searchReplace(context.Background(), args(
		"pattern", "xyzzy",
		"replacement", "replaced",
		"files", "nomatch.txt",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "no matches") {
		t.Errorf("expected no-matches message, got: %s", result.Text)
	}

	// File unchanged.
	data, _ := os.ReadFile(filepath.Join(dir, "nomatch.txt"))
	if string(data) != "nothing to see\n" {
		t.Errorf("file should be unchanged, got: %s", data)
	}
}

func TestSearchReplace_InvalidRegex(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "x.txt", "content\n")

	_, err := e.searchReplace(context.Background(), args(
		"pattern", "[invalid(",
		"replacement", "x",
		"files", "x.txt",
	))
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
	if !strings.Contains(err.Error(), "invalid regex") {
		t.Errorf("expected invalid regex error, got: %v", err)
	}
}

func TestSearchReplace_MissingPattern(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	_, err := e.searchReplace(context.Background(), args(
		"replacement", "x",
		"files", "a.txt",
	))
	if err == nil {
		t.Fatal("expected error for missing pattern")
	}
}

func TestSearchReplace_MissingFiles(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	_, err := e.searchReplace(context.Background(), args(
		"pattern", "x",
		"replacement", "y",
	))
	if err == nil {
		t.Fatal("expected error for missing files")
	}
}

func TestSearchReplace_FileNotFound(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	_, err := e.searchReplace(context.Background(), args(
		"pattern", "x",
		"replacement", "y",
		"files", "nonexistent.txt",
	))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSearchReplace_SensitivePath(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	_, err := e.searchReplace(context.Background(), args(
		"pattern", "x",
		"replacement", "y",
		"files", ".env",
	))
	if err == nil {
		t.Fatal("expected error for sensitive path")
	}
}

func TestSearchReplace_BinaryFileSkipped(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Write a file with null bytes.
	binary := []byte("hello\x00world\n")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "binary.bin"), binary, 0o644)

	_, err := e.searchReplace(context.Background(), args(
		"pattern", "hello",
		"replacement", "hi",
		"files", "binary.bin",
	))
	// Binary file being skipped should result in no-match or error.
	if err != nil {
		t.Logf("error (ok for binary): %v", err)
	}
}

func TestSearchReplace_Multiline(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "ml.txt", "first line\nsecond line\nthird line\n")

	_, err := e.searchReplace(context.Background(), args(
		"pattern", `^second`,
		"replacement", "REPLACED",
		"files", "ml.txt",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "ml.txt"))
	got := string(data)
	if !strings.Contains(got, "REPLACED line") {
		t.Errorf("expected multiline match, got: %s", got)
	}
}

func TestSearchReplace_LineRangeInMeta(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "meta.txt", "line1\nline2\nline3\nline4\nline5\n")

	result, err := e.searchReplace(context.Background(), args(
		"pattern", "line3",
		"replacement", "CHANGED",
		"files", "meta.txt",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check meta contains files array with line info.
	metaFiles, ok := result.Meta["files"].([]searchReplaceFileResult)
	if !ok {
		t.Fatalf("expected files in meta, got: %T", result.Meta["files"])
	}
	if len(metaFiles) == 0 {
		t.Fatal("expected at least one file result")
	}
	if metaFiles[0].FirstLine != 3 {
		t.Errorf("expected firstLine=3, got %d", metaFiles[0].FirstLine)
	}
	if metaFiles[0].LastLine != 3 {
		t.Errorf("expected lastLine=3, got %d", metaFiles[0].LastLine)
	}
}

func TestSearchReplace_GlobPattern(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.go", "oldFunc()\n")
	writeTestFile(t, dir, "b.go", "oldFunc()\n")
	writeTestFile(t, dir, "c.ts", "oldFunc()\n")

	result, err := e.searchReplace(context.Background(), args(
		"pattern", "oldFunc",
		"replacement", "newFunc",
		"files", []interface{}{"a.go", "b.go", "c.ts"},
		"include", "*.go",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only .go files should be affected.
	if !strings.Contains(result.Text, "2 files") {
		t.Errorf("expected 2 files (only .go), got: %s", result.Text)
	}

	dataC, _ := os.ReadFile(filepath.Join(dir, "c.ts"))
	if !strings.Contains(string(dataC), "oldFunc") {
		t.Errorf("c.ts should be unchanged, got: %s", dataC)
	}
}

func TestSearchReplace_FilesGlobPattern(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.go", "oldFunc()\n")
	writeTestFile(t, dir, "b.go", "oldFunc()\n")
	writeTestFile(t, dir, "c.ts", "oldFunc()\n")

	result, err := e.searchReplace(context.Background(), args(
		"pattern", "oldFunc",
		"replacement", "newFunc",
		"files", "*.go",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "2 files") {
		t.Errorf("expected 2 files changed, got: %s", result.Text)
	}

	for _, name := range []string{"a.go", "b.go"} {
		data, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		if !strings.Contains(string(data), "newFunc") {
			t.Errorf("%s should be changed, got: %s", name, data)
		}
	}
	dataC, _ := os.ReadFile(filepath.Join(dir, "c.ts"))
	if !strings.Contains(string(dataC), "oldFunc") {
		t.Errorf("c.ts should be unchanged, got: %s", dataC)
	}
}

func TestSearchReplace_DirectoryExpandsFiles(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "sub/a.txt", "old\n")
	writeTestFile(t, dir, "sub/b.txt", "old\n")

	result, err := e.searchReplace(context.Background(), args(
		"pattern", "old",
		"replacement", "new",
		"files", "sub",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "2 files") {
		t.Errorf("expected 2 files changed, got: %s", result.Text)
	}
}

func TestSearchReplace_GlobNoMatch(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	_, err := e.searchReplace(context.Background(), args(
		"pattern", "old",
		"replacement", "new",
		"files", "*.missing",
	))
	if err == nil {
		t.Fatal("expected error for glob with no matches")
	}
	if !strings.Contains(err.Error(), `no files matched`) {
		t.Errorf("expected no-files-matched error, got: %v", err)
	}
}

func TestSearchReplace_FilesGlobWithInclude(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.go", "old\n")
	writeTestFile(t, dir, "b.txt", "old\n")

	result, err := e.searchReplace(context.Background(), args(
		"pattern", "old",
		"replacement", "new",
		"files", "*.*",
		"include", "*.go",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "1 files") {
		t.Errorf("expected 1 file changed, got: %s", result.Text)
	}

	txt, _ := os.ReadFile(filepath.Join(dir, "b.txt"))
	if string(txt) != "old\n" {
		t.Errorf("include filter should leave b.txt unchanged, got: %q", txt)
	}
}

func TestSearchReplace_CRLFPreserved(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "crlf.txt", "hello\r\nworld\r\n")

	_, err := e.searchReplace(context.Background(), args(
		"pattern", "hello",
		"replacement", "hi",
		"files", "crlf.txt",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "crlf.txt"))
	got := string(data)
	if !strings.Contains(got, "\r\n") {
		t.Errorf("CRLF should be preserved, got: %q", got)
	}
	if !strings.Contains(got, "hi\r\n") {
		t.Errorf("expected 'hi\\r\\n', got: %q", got)
	}
}

func TestSearchReplace_ReplaceAllOccurrences(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// Write a file with many occurrences.
	writeTestFile(t, dir, "many.txt", "x x x x x x x x x x\n")

	result, err := e.searchReplace(context.Background(), args(
		"pattern", "x",
		"replacement", "y",
		"files", "many.txt",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "10 replacements") {
		t.Errorf("expected 10 replacements, got: %s", result.Text)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "many.txt"))
	got := string(data)
	if strings.Contains(got, "x") {
		t.Errorf("all x should be replaced, got: %s", got)
	}
}

func TestSearchReplace_NoChangeDetection(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "nochange.txt", "hello world\n")

	_, err := e.searchReplace(context.Background(), args(
		"pattern", "hello",
		"replacement", "hello", // same as match
		"files", "nochange.txt",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSearchReplace_EmptyReplacement(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "empty.txt", "keep REMOVE keep\n")

	_, err := e.searchReplace(context.Background(), args(
		"pattern", ` REMOVE `,
		"replacement", " ",
		"files", "empty.txt",
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "empty.txt"))
	if string(data) != "keep keep\n" {
		t.Errorf("expected 'keep keep', got: %q", data)
	}
}
