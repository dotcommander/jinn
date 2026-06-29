package jinn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// lspArgs builds a map[string]interface{} for lspQueryWithLauncher calls.
// int values are promoted to float64 because intArg() only matches float64
// (mirroring JSON number unmarshalling). String/bool values pass through unchanged.
func lspArgs(kv ...any) map[string]interface{} {
	m := make(map[string]interface{}, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		v := kv[i+1]
		if n, ok := v.(int); ok {
			v = float64(n)
		}
		m[kv[i].(string)] = v
	}
	return m
}

// writeLSPFile creates a placeholder Go source file that satisfies
// lspClient.didOpen (which reads the file from disk). The content is
// irrelevant to the mock server; only the path extension matters.
func writeLSPFile(t *testing.T, dir, name string) {
	t.Helper()
	writeTestFile(t, dir, name, "package main\n")
}

func TestLSPDiagnosticsGoUsesGoplsCheckForValidFile(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	e := New(wd, "dev")

	out, err := e.lspQuery(context.Background(), lspArgs(
		"action", "diagnostics",
		"path", "tool_shell.go",
	))
	if err != nil {
		t.Fatalf("unexpected diagnostics error: %v", err)
	}
	if out != "no diagnostics found" {
		t.Fatalf("expected no diagnostics for compiling Go file, got:\n%s", out)
	}
}

// --- happy-path tests (all parallel — they inject a launcher, no global state) ---

func TestLSP_InitializeIncludesWorkspaceFolder(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.go")

	initParams := make(chan map[string]any, 1)
	_, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "diagnostics",
		"path", "src.go",
	), newMockLauncherCfg(mockConfig{initializeParams: initParams}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var params map[string]any
	select {
	case params = <-initParams:
	case <-time.After(time.Second):
		t.Fatal("mock server did not capture initialize params")
	}

	rootURI := pathToURI(dir)
	if got, _ := params["rootPath"].(string); got != dir {
		t.Fatalf("rootPath = %q, want %q", got, dir)
	}
	if got, _ := params["rootUri"].(string); got != rootURI {
		t.Fatalf("rootUri = %q, want %q", got, rootURI)
	}
	folders, ok := params["workspaceFolders"].([]any)
	if !ok || len(folders) != 1 {
		t.Fatalf("workspaceFolders = %#v, want one folder", params["workspaceFolders"])
	}
	folder, ok := folders[0].(map[string]any)
	if !ok {
		t.Fatalf("workspaceFolders[0] = %#v, want object", folders[0])
	}
	if got, _ := folder["uri"].(string); got != rootURI {
		t.Fatalf("workspaceFolders[0].uri = %q, want %q", got, rootURI)
	}
	if got, _ := folder["name"].(string); got != filepath.Base(dir) {
		t.Fatalf("workspaceFolders[0].name = %q, want %q", got, filepath.Base(dir))
	}
}

func TestLSP_Definition_Succeeds(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.go")

	out, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "definition",
		"path", "src.go",
		"line", 1,
		"character", 1,
	), newMockLauncher(false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// mock replies with file:///fake/src.go, line 9 (0-based) → 10, char 0 → 1
	// After LSP enhancements: definition returns relative paths + header
	if !strings.Contains(out, "1 location(s) found") {
		t.Errorf("expected header in output, got: %q", out)
	}
	if !strings.Contains(out, "/fake/src.go:10:1") {
		t.Errorf("expected position /fake/src.go:10:1 in output, got: %q", out)
	}
}

func TestLSP_References_Returns3Locations(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.go")

	out, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "references",
		"path", "src.go",
		"line", 1,
		"character", 1,
	), newMockLauncher(false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// mock returns 3 locations: file:///fake/ref0.go, ref1.go, ref2.go
	// After LSP enhancements: references returns header + relative paths
	if !strings.Contains(out, "3 location(s) found") {
		t.Errorf("expected header in output, got: %q", out)
	}
	for i := 0; i < 3; i++ {
		want := fmt.Sprintf("/fake/ref%d.go", i)
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %q", want, out)
		}
	}
}

func TestLSP_Hover_ReturnsMarkdown(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.go")

	out, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "hover",
		"path", "src.go",
		"line", 1,
		"character", 1,
	), newMockLauncher(false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const want = "func Foo() error"
	if !strings.Contains(out, want) {
		t.Errorf("expected %q in hover output, got: %q", want, out)
	}
}

func TestLSP_Symbols_FormattedTable(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.go")

	out, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "symbols",
		"path", "src.go",
	), newMockLauncher(false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// mock returns Foo (kind 12 = Function, line 4) and Bar (kind 13 = Variable, line 9)
	if !strings.Contains(out, "Function") || !strings.Contains(out, "Foo") {
		t.Errorf("expected Function/Foo in symbols output, got: %q", out)
	}
	if !strings.Contains(out, "Variable") || !strings.Contains(out, "Bar") {
		t.Errorf("expected Variable/Bar in symbols output, got: %q", out)
	}
	// each symbol occupies one line — two symbols → two lines
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 symbol lines, got %d:\n%s", len(lines), out)
	}
	// After LSP enhancements: hierarchical format uses (line N) instead of (N:M)
	if !strings.Contains(out, "(line 5)") {
		t.Errorf("expected Foo at (line 5), got: %q", out)
	}
	if !strings.Contains(out, "(line 10)") {
		t.Errorf("expected Bar at (line 10), got: %q", out)
	}
}

func TestLSP_Diagnostics_ReturnsItems(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.go")

	out, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "diagnostics",
		"path", "src.go",
	), newMockLauncher(false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "1 diagnostic(s) found") {
		t.Errorf("expected diagnostics header in output, got: %q", out)
	}
	if !strings.Contains(out, "error mock-lsp E100: mock diagnostic") {
		t.Errorf("expected formatted diagnostic in output, got: %q", out)
	}
	if !strings.Contains(out, ":3:5:") {
		t.Errorf("expected 1-based diagnostic position in output, got: %q", out)
	}
}

func TestLSP_Diagnostics_NullResponse(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.go")

	out, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "diagnostics",
		"path", "src.go",
	), newMockLauncherCfg(mockConfig{nullDiagnostics: true}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "no diagnostics found" {
		t.Errorf("expected no diagnostics message, got: %q", out)
	}
}

// --- error-path tests ---

func TestLSP_UnknownExtension_ErrWithSuggestion(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.xyz")

	_, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "definition",
		"path", "src.xyz",
		"line", 1,
		"character", 1,
	), newMockLauncher(false))
	if err == nil {
		t.Fatal("expected error for unknown extension, got nil")
	}
	var sug *ErrWithSuggestion
	if !errors.As(err, &sug) {
		t.Fatalf("expected *ErrWithSuggestion, got: %T %v", err, err)
	}
	if !strings.Contains(sug.Suggestion, "supported") {
		t.Errorf("suggestion should mention supported extensions, got: %q", sug.Suggestion)
	}
}

func TestLSP_MissingArgs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		argMap  map[string]interface{}
		wantMsg string
	}{
		{
			name:    "missing action",
			argMap:  lspArgs("path", "src.go", "line", 1, "character", 1),
			wantMsg: "action",
		},
		{
			name:    "missing path",
			argMap:  lspArgs("action", "definition", "line", 1, "character", 1),
			wantMsg: "path",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, _ := testEngine(t)
			_, err := e.lspQueryWithLauncher(context.Background(), tc.argMap, newMockLauncher(false))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var sug *ErrWithSuggestion
			if !errors.As(err, &sug) {
				t.Fatalf("expected *ErrWithSuggestion, got: %T %v", err, err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("expected %q in error message, got: %q", tc.wantMsg, err.Error())
			}
		})
	}
}

// TestLSP_MissingServerBinary verifies that a launcher returning exec.ErrNotFound
// produces an ErrWithSuggestion with an install hint. The launcher is injected
// directly — no real PATH lookup occurs.
//
// This test is parallel — timeout is now per-Engine, not a package-level variable.
func TestLSP_MissingServerBinary(t *testing.T) {
	t.Parallel()

	e, dir := testEngine(t)
	e.LSPTimeoutSec = 2
	writeLSPFile(t, dir, "src.go")

	notFound := &exec.Error{Name: "gopls", Err: exec.ErrNotFound}
	launcher := fakeLauncherError(notFound)

	_, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "definition",
		"path", "src.go",
		"line", 1,
		"character", 1,
	), launcher)
	if err == nil {
		t.Fatal("expected error from missing server binary, got nil")
	}
	// The error bubbles from client.start — it wraps the launcher's error.
	// We accept any error that mentions the missing binary or a suggestion to install.
	msg := err.Error()
	if !strings.Contains(msg, "gopls") && !strings.Contains(msg, "not found") && !strings.Contains(msg, "install") {
		t.Errorf("expected error to mention missing binary or install hint, got: %q", msg)
	}
}

// TestLSP_Timeout verifies that lspQueryWithLauncher returns a timeout error
// when the server never responds.
func TestLSP_Timeout(t *testing.T) {
	t.Parallel()

	e, dir := testEngine(t)
	e.LSPTimeoutSec = 1
	writeLSPFile(t, dir, "src.go")

	_, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "definition",
		"path", "src.go",
		"line", 1,
		"character", 1,
	), newMockLauncher(true /* slow */))
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected 'timed out' in error, got: %q", err.Error())
	}
}

// TestLSP_ShutdownCleanPipeClose verifies that the mock server exits cleanly
// after the client sends shutdown+exit — no goroutine hang, pipes drained.
func TestLSP_ShutdownCleanPipeClose(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.go")

	done := make(chan error, 1)
	go func() {
		_, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
			"action", "hover",
			"path", "src.go",
			"line", 1,
			"character", 1,
		), newMockLauncher(false))
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lspQueryWithLauncher hung — pipes not closed cleanly on shutdown")
	}
}

// --- findSymbolColumn unit tests ---

func TestFindSymbolColumn_Found(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "src.go")
	// "func Foo() error" — "Foo" starts at byte/rune 5.
	if err := os.WriteFile(path, []byte("func Foo() error\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	col, err := findSymbolColumn(path, 0, "Foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if col != 5 {
		t.Errorf("expected column 5, got %d", col)
	}
}

func TestFindSymbolColumn_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "src.go")
	if err := os.WriteFile(path, []byte("func Foo() error\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := findSymbolColumn(path, 0, "Bar")
	if err == nil {
		t.Fatal("expected error for missing symbol, got nil")
	}
	if !strings.Contains(err.Error(), "Bar") {
		t.Errorf("error should mention symbol name, got: %q", err.Error())
	}
}

func TestFindSymbolColumn_LineOutOfBounds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "src.go")
	if err := os.WriteFile(path, []byte("func Foo() error\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := findSymbolColumn(path, 99, "Foo")
	if err == nil {
		t.Fatal("expected error for out-of-bounds line, got nil")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("error should mention out of range, got: %q", err.Error())
	}
}

func TestFindSymbolColumn_Unicode(t *testing.T) {
	t.Parallel()
	// "αβγ Foo" — three BMP runes before the space, then "Foo".
	// BMP rune count and UTF-16 code-unit count are both 4 here.
	dir := t.TempDir()
	path := filepath.Join(dir, "src.go")
	if err := os.WriteFile(path, []byte("αβγ Foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	col, err := findSymbolColumn(path, 0, "Foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if col != 4 {
		t.Errorf("expected UTF-16 column 4, got %d", col)
	}
}

func TestFindSymbolColumn_UTF16Astral(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "src.go")
	if err := os.WriteFile(path, []byte("😀 Foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	col, err := findSymbolColumn(path, 0, "Foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if col != 3 {
		t.Errorf("expected UTF-16 column 3, got %d", col)
	}
}

func TestFindSymbolColumn_CRLF(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "src.go")
	if err := os.WriteFile(path, []byte("package main\r\nfunc Foo() {}\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	col, err := findSymbolColumn(path, 1, "Foo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if col != 5 {
		t.Errorf("expected column 5, got %d", col)
	}
}

func TestGoASTSymbols_ShallowOutline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "src.go")
	src := strings.Join([]string{
		"package main",
		"type Config struct{}",
		"type Runner interface{ Run() }",
		"const Answer = 42",
		"var Value = Answer",
		"func Top() {",
		"  func() {}()",
		"}",
		"func (Config) Method() {}",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := goASTSymbols(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"Struct Config (line 2)",
		"Interface Runner (line 3)",
		"Constant Answer (line 4)",
		"Variable Value (line 5)",
		"Function Top (line 6)",
		"Method Method (line 9)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "anonymous") {
		t.Errorf("fallback outline should stay shallow, got:\n%s", out)
	}
}

func TestLSPGoSymbolsFallbackWithoutGopls(t *testing.T) {
	dir := t.TempDir()
	e := New(dir, "dev")
	t.Setenv("PATH", "")
	writeTestFile(t, dir, "src.go", strings.Join([]string{
		"package main",
		"type Config struct{}",
		"func Top() {}",
		"",
	}, "\n"))

	out, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "symbols",
		"path", "src.go",
	), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Struct Config (line 2)") {
		t.Errorf("expected Go AST struct symbol in output:\n%s", out)
	}
	if !strings.Contains(out, "Function Top (line 3)") {
		t.Errorf("expected Go AST function symbol in output:\n%s", out)
	}
}

// --- lspFormatContext unit tests ---

func TestLspFormatContext_NormalCase(t *testing.T) {
	t.Parallel()
	lines := []string{"line1", "line2", "line3", "line4", "line5"}
	// target=2 (0-based, "line3"), contextSize=2 → lines 0-4 all included.
	out := lspFormatContext(lines, 2, 2)
	if !strings.Contains(out, "> ") {
		t.Errorf("expected marker '> ' on target line, got:\n%s", out)
	}
	// Target line must carry the ">" marker.
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "line3") && !strings.HasPrefix(ln, ">") {
			t.Errorf("target line 'line3' should have '>' marker, got: %q", ln)
		}
	}
	// Non-target lines must not carry ">".
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "line1") && strings.HasPrefix(ln, ">") {
			t.Errorf("non-target line 'line1' should not have '>' marker, got: %q", ln)
		}
	}
}

func TestLspFormatContext_NearStart(t *testing.T) {
	t.Parallel()
	lines := []string{"line1", "line2", "line3", "line4", "line5"}
	// target=0 (first line), contextSize=2 → only lines 0-2 can be shown.
	out := lspFormatContext(lines, 0, 2)
	if !strings.Contains(out, "line1") {
		t.Errorf("expected line1 in output, got:\n%s", out)
	}
	// Should not include negative-index lines.
	lineCount := len(strings.Split(strings.TrimSpace(out), "\n"))
	if lineCount > 3 {
		t.Errorf("expected at most 3 lines near start, got %d:\n%s", lineCount, out)
	}
}

func TestLspFormatContext_NearEnd(t *testing.T) {
	t.Parallel()
	lines := []string{"line1", "line2", "line3", "line4", "line5"}
	// target=4 (last line), contextSize=2 → lines 2-4.
	out := lspFormatContext(lines, 4, 2)
	if !strings.Contains(out, "line5") {
		t.Errorf("expected line5 in output, got:\n%s", out)
	}
	lineCount := len(strings.Split(strings.TrimSpace(out), "\n"))
	if lineCount > 3 {
		t.Errorf("expected at most 3 lines near end, got %d:\n%s", lineCount, out)
	}
}

func TestLspFormatContext_EmptyLines(t *testing.T) {
	t.Parallel()
	out := lspFormatContext(nil, 0, 2)
	if out != "" {
		t.Errorf("expected empty output for nil lines, got: %q", out)
	}
	out = lspFormatContext([]string{}, 0, 2)
	if out != "" {
		t.Errorf("expected empty output for empty lines, got: %q", out)
	}
}

func TestLspFormatContext_ZeroContext(t *testing.T) {
	t.Parallel()
	lines := []string{"line1", "line2", "line3"}
	out := lspFormatContext(lines, 1, 0)
	if out != "" {
		t.Errorf("expected empty output for contextSize=0, got: %q", out)
	}
}

// --- formatWorkspaceEdit unit tests ---

func TestFormatWorkspaceEdit_SingleFile(t *testing.T) {
	t.Parallel()
	edit := &lspWorkspaceEdit{
		Changes: map[string][]lspTextEdit{
			"file:///work/main.go": {
				{Range: lspRange{Start: struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				}{Line: 4, Character: 0}}, NewText: "renamedFoo"},
			},
		},
	}
	out := formatWorkspaceEdit(edit, "/work")
	if !strings.Contains(out, "main.go") {
		t.Errorf("expected filename in output, got: %q", out)
	}
	if !strings.Contains(out, "renamedFoo") {
		t.Errorf("expected new text in output, got: %q", out)
	}
	if !strings.Contains(out, "line 5") {
		t.Errorf("expected 'line 5' (0-based 4 → 1-based 5) in output, got: %q", out)
	}
	if !strings.Contains(out, "1 file(s)") {
		t.Errorf("expected '1 file(s)' summary, got: %q", out)
	}
}

func TestFormatWorkspaceEdit_MultipleFiles(t *testing.T) {
	t.Parallel()
	edit := &lspWorkspaceEdit{
		Changes: map[string][]lspTextEdit{
			"file:///work/a.go": {
				{Range: lspRange{}, NewText: "newA"},
			},
			"file:///work/b.go": {
				{Range: lspRange{}, NewText: "newB1"},
				{Range: lspRange{}, NewText: "newB2"},
			},
		},
	}
	out := formatWorkspaceEdit(edit, "/work")
	if !strings.Contains(out, "2 file(s)") {
		t.Errorf("expected '2 file(s)' summary, got: %q", out)
	}
	if !strings.Contains(out, "3 edit(s) total") {
		t.Errorf("expected '3 edit(s) total' summary, got: %q", out)
	}
	// Both filenames must appear.
	if !strings.Contains(out, "a.go") || !strings.Contains(out, "b.go") {
		t.Errorf("expected both filenames in output, got: %q", out)
	}
}

func TestFormatWorkspaceEdit_Empty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit *lspWorkspaceEdit
	}{
		{"nil", nil},
		{"empty changes", &lspWorkspaceEdit{Changes: map[string][]lspTextEdit{}}},
		{"empty document changes", &lspWorkspaceEdit{DocumentChanges: []lspTextDocumentEdit{{}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := formatWorkspaceEdit(tc.edit, "/work")
			if out != "no changes" {
				t.Errorf("expected 'no changes', got: %q", out)
			}
		})
	}
}

func TestFormatWorkspaceEdit_DocumentChanges(t *testing.T) {
	t.Parallel()
	var change lspTextDocumentEdit
	change.TextDocument.URI = "file:///work/doc.go"
	change.Edits = []lspTextEdit{
		{Range: lspRange{Start: struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		}{Line: 2, Character: 0}}, NewText: "docRename"},
	}
	edit := &lspWorkspaceEdit{DocumentChanges: []lspTextDocumentEdit{change}}

	out := formatWorkspaceEdit(edit, "/work")
	if !strings.Contains(out, "doc.go") {
		t.Errorf("expected documentChanges filename in output, got: %q", out)
	}
	if !strings.Contains(out, "line 3") {
		t.Errorf("expected documentChanges line in output, got: %q", out)
	}
	if !strings.Contains(out, "docRename") {
		t.Errorf("expected documentChanges edit text in output, got: %q", out)
	}
}

// --- unmarshalLocations unit tests ---

func TestUnmarshalLocations_SliceOfLocations(t *testing.T) {
	t.Parallel()
	raw, _ := json.Marshal([]lspLocation{
		{URI: "file:///a.go"},
		{URI: "file:///b.go"},
	})
	locs := unmarshalLocations(raw)
	if len(locs) != 2 {
		t.Fatalf("expected 2 locations, got %d", len(locs))
	}
	uris := []string{locs[0].URI, locs[1].URI}
	sort.Strings(uris)
	if uris[0] != "file:///a.go" || uris[1] != "file:///b.go" {
		t.Errorf("unexpected URIs: %v", uris)
	}
}

func TestUnmarshalLocations_SingleLocation(t *testing.T) {
	t.Parallel()
	raw, _ := json.Marshal(lspLocation{URI: "file:///single.go"})
	locs := unmarshalLocations(raw)
	if len(locs) != 1 {
		t.Fatalf("expected 1 location, got %d", len(locs))
	}
	if locs[0].URI != "file:///single.go" {
		t.Errorf("expected file:///single.go, got %q", locs[0].URI)
	}
}

// TestUnmarshalLocations_LocationLinks verifies the LocationLink branch.
func TestUnmarshalLocations_LocationLinks(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`[{"targetUri":"file:///link.go","targetRange":{"start":{"line":7,"character":3}},"targetSelectionRange":{"start":{"line":7,"character":3}}}]`)
	locs := unmarshalLocations(raw)
	if len(locs) != 1 {
		t.Fatalf("expected 1 location entry, got %d", len(locs))
	}
	if locs[0].URI != "file:///link.go" {
		t.Errorf("expected LocationLink target URI, got %q", locs[0].URI)
	}
	if locs[0].Range.Start.Line != 7 || locs[0].Range.Start.Character != 3 {
		t.Errorf("expected LocationLink target range start, got %+v", locs[0].Range.Start)
	}
}

func TestUnmarshalLocations_InvalidJSON(t *testing.T) {
	t.Parallel()
	locs := unmarshalLocations(json.RawMessage(`not json`))
	if locs != nil {
		t.Errorf("expected nil for invalid JSON, got %v", locs)
	}
}

func TestUnmarshalLocations_Null(t *testing.T) {
	t.Parallel()
	locs := unmarshalLocations(json.RawMessage(`null`))
	if locs != nil {
		t.Errorf("expected nil for null result, got %v", locs)
	}
}

// --- rename via mock server ---

func TestLSP_Rename_WithNewName(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.go")

	out, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "rename",
		"path", "src.go",
		"line", 1,
		"character", 1,
		"new_name", "newName",
	), newMockLauncher(false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "newName") {
		t.Errorf("expected new name in rename output, got: %q", out)
	}
	if !strings.Contains(out, "edit(s)") {
		t.Errorf("expected edit summary in rename output, got: %q", out)
	}
}

func TestLSP_Rename_MissingNewName(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.go")

	_, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "rename",
		"path", "src.go",
		"line", 1,
		"character", 1,
		// new_name intentionally omitted
	), newMockLauncher(false))
	if err == nil {
		t.Fatal("expected error for missing new_name, got nil")
	}
	var sug *ErrWithSuggestion
	if !errors.As(err, &sug) {
		t.Fatalf("expected *ErrWithSuggestion, got: %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "new_name") {
		t.Errorf("expected 'new_name' in error, got: %q", err.Error())
	}
}

// --- ext table coverage ---

func TestLspServerForExt_KnownExtensions(t *testing.T) {
	t.Parallel()
	// These extensions must be in the table. We only check for table membership,
	// not binary presence — lspServerForExt returns ErrWithSuggestion with an
	// install hint when the binary is absent, which is a different error path.
	knownExts := []string{".c", ".cpp", ".java", ".lua", ".zig", ".go", ".rs", ".py", ".ts", ".js"}
	for _, ext := range knownExts {
		t.Run(ext, func(t *testing.T) {
			t.Parallel()
			_, err := lspServerForExt(ext)
			if err == nil {
				return // binary present — great
			}
			var sug *ErrWithSuggestion
			if !errors.As(err, &sug) {
				t.Fatalf("ext %s: expected *ErrWithSuggestion for missing binary, got: %T %v", ext, err, err)
			}
			// Must be the "not found on PATH" error, not the "unknown extension" error.
			if strings.Contains(sug.Error(), "no LSP server known") {
				t.Errorf("ext %s is not in lspExtTable", ext)
			}
		})
	}
}

func TestLspServerForExt_UnknownExtension(t *testing.T) {
	t.Parallel()
	_, err := lspServerForExt(".unknownxyz")
	if err == nil {
		t.Fatal("expected error for unknown extension, got nil")
	}
	var sug *ErrWithSuggestion
	if !errors.As(err, &sug) {
		t.Fatalf("expected *ErrWithSuggestion, got: %T %v", err, err)
	}
	if !strings.Contains(sug.Error(), "no LSP server known") {
		t.Errorf("expected 'no LSP server known' in error, got: %q", sug.Error())
	}
	if !strings.Contains(sug.Suggestion, "supported") {
		t.Errorf("suggestion should list supported extensions, got: %q", sug.Suggestion)
	}
}

// --- didOpen size guard ---

func TestLSP_DidOpenSizeGuard(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)

	// Write a file that exceeds maxLSPFileSize.
	path := filepath.Join(dir, "big.go")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// Seek past the limit and write one byte to create a sparse file of the right size.
	if _, wErr := f.WriteAt([]byte{0}, maxLSPFileSize+1); wErr != nil {
		_ = f.Close()
		t.Fatal(wErr)
	}
	_ = f.Close()

	_, err = e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "hover",
		"path", "big.go",
		"line", 1,
		"character", 1,
	), newMockLauncher(false))
	if err == nil {
		t.Fatal("expected error for oversized file, got nil")
	}
	var sug *ErrWithSuggestion
	if !errors.As(err, &sug) {
		t.Fatalf("expected *ErrWithSuggestion, got: %T %v", err, err)
	}
	if sug.Code != ErrCodeFileTooLarge {
		t.Errorf("expected ErrCodeFileTooLarge code, got: %q", sug.Code)
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected 'too large' in error message, got: %q", err.Error())
	}
}
