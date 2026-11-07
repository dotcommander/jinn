package jinn

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamFileSHA256_MatchesBuffered(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	content := strings.Repeat("stream-me ", 256)
	writeTestFile(t, dir, "big.txt", content)

	digest, exists, err := e.streamFileSHA256(filepath.Join(dir, "big.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("expected exists=true for a regular file")
	}
	if digest != sha256Hex([]byte(content)) {
		t.Errorf("stream digest = %q, want %q", digest, sha256Hex([]byte(content)))
	}

	_, exists, err = e.streamFileSHA256(filepath.Join(dir, "missing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("expected exists=false for a missing file")
	}
}

func TestWriteFileIfAbsentRace_NoReplacePublish(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	target := "created-race.txt"

	// Target appears after preflight; the before-commit recheck rejects the
	// write and the externally created bytes must survive untouched.
	_, err := e.writeFileWithPreCommitHook(
		args("path", target, "content", "agent content", "if_absent", true),
		func() {
			_ = os.WriteFile(filepath.Join(dir, target), []byte("externally created"), 0o640)
		},
	)
	requireStaleFileError(t, err)
	got, readErr := os.ReadFile(filepath.Join(dir, target))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "externally created" {
		t.Fatalf("exclusive create overwrote the racing target: %q", got)
	}

	// Direct staged-publish firewall: even if the target appears after the
	// recheck, noReplace publish must fail with ErrCodeStaleFile instead of
	// rename-overwriting.
	resolved, resErr := e.checkPathForMutation(target)
	if resErr != nil {
		t.Fatal(resErr)
	}
	_, pubErr := e.atomicWriteFileStaged(resolved, "second writer", true, nil)
	var sErr *ErrWithSuggestion
	if !errors.As(pubErr, &sErr) || sErr.Code != ErrCodeStaleFile {
		t.Fatalf("expected ErrCodeStaleFile from noReplace publish, got: %v", pubErr)
	}
	got, _ = os.ReadFile(filepath.Join(dir, target))
	if string(got) != "externally created" {
		t.Fatalf("noReplace publish overwrote: %q", got)
	}
}

func TestWriteFileIfAbsent_CreateSucceeds(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	if _, err := e.writeFile(args("path", "brand-new.txt", "content", "hello", "if_absent", true)); err != nil {
		t.Fatalf("exclusive create should succeed: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "brand-new.txt"))
	if string(got) != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
}
