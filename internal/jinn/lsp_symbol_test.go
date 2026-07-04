package jinn

import (
	"context"
	"strings"
	"testing"
)

// Symbol-only references resolves Foo's position from document symbols, then
// returns the reference set at the resolved position.
func TestLSP_References_SymbolOnly_Succeeds(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.go")
	out, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "references", "path", "src.go", "symbol", "Foo",
	), newMockLauncher(false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "3 location(s) found") {
		t.Fatalf("want 3 locations, got: %q", out)
	}
}

// Symbol-only definition resolves the position the same way.
func TestLSP_Definition_SymbolOnly_Succeeds(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.go")
	out, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "definition", "path", "src.go", "symbol", "Bar",
	), newMockLauncher(false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "1 location(s) found") {
		t.Fatalf("want a definition location, got: %q", out)
	}
}

// A symbol name matching no declaration returns a clear error instead of
// guessing a position.
func TestLSP_SymbolOnly_NotFound(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.go")
	_, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "references", "path", "src.go", "symbol", "Nonexistent",
	), newMockLauncher(false))
	if err == nil {
		t.Fatal("want error for unknown symbol, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want 'not found' error, got: %v", err)
	}
}

// Regression: omitting both line and symbol still fails fast at parse time.
func TestLSP_NoLineNoSymbol_Rejected(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.go")
	_, err := e.lspQueryWithLauncher(context.Background(), lspArgs(
		"action", "references", "path", "src.go",
	), newMockLauncher(false))
	if err == nil {
		t.Fatal("want error when neither line nor symbol is given, got nil")
	}
	if !strings.Contains(err.Error(), "'line' is required") {
		t.Fatalf("want line-required error, got: %v", err)
	}
}

// collectSymbolMatches finds every declaration with the target name, including
// nested children — the mechanism behind ambiguous-symbol detection.
func TestCollectSymbolMatches_Duplicates(t *testing.T) {
	t.Parallel()
	syms := []lspDocSymbol{
		{Name: "Dup"},
		{Name: "Other", Children: []lspDocSymbol{{Name: "Dup"}}},
	}
	var got []lspDocSymbol
	collectSymbolMatches(syms, "Dup", &got)
	if len(got) != 2 {
		t.Fatalf("want 2 matches (incl. nested), got %d", len(got))
	}
}
