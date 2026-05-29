package jinn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// shimSleepBinary writes a POSIX shim at <dir>/<name> that sleeps far longer
// than any test timeout, used to simulate a wedged rg/grep/fd/find subprocess.
func shimSleepBinary(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	// `exec sleep` replaces the shell so SIGKILL hits one PID cleanly.
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatalf("write shim %s: %v", name, err)
	}
	return path
}

// TestSearchFilesTimeout verifies that a wedged rg/grep subprocess surfaces
// as ErrCodeTimeout, distinct from a normal no-match result.
func TestSearchFilesTimeout(t *testing.T) {
	// Not parallel: mutates process-global searchTimeout.
	if runtime.GOOS == "windows" {
		t.Skip("shim assumes POSIX shell")
	}

	_, dir := testEngine(t)
	shimDir := t.TempDir()
	rgShim := shimSleepBinary(t, shimDir, "rg")

	e := New(dir, "dev")
	e.rgPath = rgShim // force the rg path with our slow shim

	orig := searchTimeout
	searchTimeout = 200 * time.Millisecond
	t.Cleanup(func() { searchTimeout = orig })

	start := time.Now()
	_, err := e.searchFiles(args("pattern", "anything", "path", "."))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	var sug *ErrWithSuggestion
	if !errors.As(err, &sug) {
		t.Fatalf("expected *ErrWithSuggestion, got %T: %v", err, err)
	}
	if sug.Code != ErrCodeTimeout {
		t.Fatalf("expected Code=%q, got %q (err=%v)", ErrCodeTimeout, sug.Code, err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected 'timed out' in error, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("searchFiles did not honor timeout: elapsed=%s", elapsed)
	}
}

// TestSearchFilesNoMatchNotTimeout guards the no-match-vs-timeout boundary:
// a normal no-match must NOT produce an ErrCodeTimeout error.
func TestSearchFilesNoMatchNotTimeout(t *testing.T) {
	t.Parallel()
	_, dir := testEngine(t)
	writeTestFile(t, dir, "a.go", "package main")
	e := New(dir, "dev")

	result, err := e.searchFiles(args(
		"pattern", "this_pattern_will_not_match_anything_xyz",
		"path", ".",
	))
	if err != nil {
		t.Fatalf("no-match should not error, got: %v", err)
	}
	// Text format zero-match result includes the [no matches: ...] sentinel.
	if !strings.Contains(result, "no matches") {
		t.Fatalf("expected 'no matches' marker, got: %q", result)
	}
}

// TestFindFilesTimeout verifies that a wedged fd/find subprocess surfaces
// as ErrCodeTimeout, distinct from a normal no-match result.
func TestFindFilesTimeout(t *testing.T) {
	// Not parallel: mutates process-global findTimeout.
	if runtime.GOOS == "windows" {
		t.Skip("shim assumes POSIX shell")
	}

	_, dir := testEngine(t)
	shimDir := t.TempDir()
	fdShim := shimSleepBinary(t, shimDir, "fd")

	e := New(dir, "dev")
	e.fdPath = fdShim // force the fd path with our slow shim

	orig := findTimeout
	findTimeout = 200 * time.Millisecond
	t.Cleanup(func() { findTimeout = orig })

	start := time.Now()
	_, err := e.findFiles(context.Background(), args("pattern", "*.go"))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	var sug *ErrWithSuggestion
	if !errors.As(err, &sug) {
		t.Fatalf("expected *ErrWithSuggestion, got %T: %v", err, err)
	}
	if sug.Code != ErrCodeTimeout {
		t.Fatalf("expected Code=%q, got %q (err=%v)", ErrCodeTimeout, sug.Code, err)
	}
	if !strings.Contains(err.Error(), "fd") {
		t.Fatalf("expected backend 'fd' in error, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("findFiles did not honor timeout: elapsed=%s", elapsed)
	}
}

// TestFindFilesNoMatchNotTimeout: a normal no-match returns an empty result,
// not an ErrCodeTimeout error.
func TestFindFilesNoMatchNotTimeout(t *testing.T) {
	t.Parallel()
	_, dir := testEngine(t)
	writeTestFile(t, dir, "main.go", "package main")
	e := New(dir, "dev")

	result, err := e.findFiles(context.Background(), args("pattern", "*.xyz"))
	if err != nil {
		t.Fatalf("no-match should not error, got: %v", err)
	}
	res := parseFindResult(t, result)
	if len(res.Files) != 0 {
		t.Fatalf("expected 0 files for no-match, got %d", len(res.Files))
	}
}

// Compile-time check: context.DeadlineExceeded is the sentinel we return.
var _ = context.DeadlineExceeded
