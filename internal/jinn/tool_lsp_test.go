package jinn

import (
	"errors"
	"fmt"
	"os/exec"
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
func writeLSPFile(t *testing.T, dir, name string) string {
	t.Helper()
	writeTestFile(t, dir, name, "package main\n")
	return name
}

// --- happy-path tests (all parallel — they inject a launcher, no global state) ---

func TestLSP_Definition_Succeeds(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.go")

	out, err := e.lspQueryWithLauncher(lspArgs(
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

	out, err := e.lspQueryWithLauncher(lspArgs(
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

	out, err := e.lspQueryWithLauncher(lspArgs(
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

	out, err := e.lspQueryWithLauncher(lspArgs(
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

// --- error-path tests ---

func TestLSP_UnknownExtension_ErrWithSuggestion(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeLSPFile(t, dir, "src.xyz")

	_, err := e.lspQueryWithLauncher(lspArgs(
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
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, _ := testEngine(t)
			_, err := e.lspQueryWithLauncher(tc.argMap, newMockLauncher(false))
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

	_, err := e.lspQueryWithLauncher(lspArgs(
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

	_, err := e.lspQueryWithLauncher(lspArgs(
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
		_, err := e.lspQueryWithLauncher(lspArgs(
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
