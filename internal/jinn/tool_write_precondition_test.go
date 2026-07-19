package jinn

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestWriteFile_IfChecksum_Match(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "old content")

	sum := sha256Hex([]byte("old content"))
	if _, err := e.writeFile(args("path", "a.txt", "content", "new content", "if_checksum", sum)); err != nil {
		t.Fatalf("expected write to succeed, got: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(got) != "new content" {
		t.Errorf("content = %q, want %q", got, "new content")
	}
}

func TestWriteFile_IfChecksum_Mismatch(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "current content")

	stale := sha256Hex([]byte("what I read earlier"))
	_, err := e.writeFile(args("path", "a.txt", "content", "clobber", "if_checksum", stale))
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) || sErr.Code != ErrCodeStaleFile {
		t.Fatalf("expected ErrCodeStaleFile, got: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(got) != "current content" {
		t.Errorf("file was modified despite stale checksum: %q", got)
	}
}

func TestWriteFile_IfChecksum_FileDeleted(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	sum := sha256Hex([]byte("was there"))
	_, err := e.writeFile(args("path", "gone.txt", "content", "x", "if_checksum", sum))
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) || sErr.Code != ErrCodeStaleFile {
		t.Fatalf("expected ErrCodeStaleFile for missing file, got: %v", err)
	}
}

func TestWriteFile_IfChecksum_CrossEngine(t *testing.T) {
	t.Parallel()
	// Two Engine instances simulate jinn's real one-process-per-call shape:
	// the read and the write never share an in-memory tracker.
	e1, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "version 1")

	res, err := e1.readFile(args("path", "a.txt", "include_checksum", true))
	if err != nil {
		t.Fatal(err)
	}
	sum, _ := res.Meta["sha256"].(string)
	if sum == "" {
		t.Fatal("read_file returned no sha256 meta")
	}

	// External writer mutates the file between the read and the write.
	writeTestFile(t, dir, "a.txt", "version 2 (external)")

	e2 := New(dir, "dev")
	_, err = e2.writeFile(args("path", "a.txt", "content", "agent version", "if_checksum", sum))
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) || sErr.Code != ErrCodeStaleFile {
		t.Fatalf("expected ErrCodeStaleFile across engines, got: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(got) != "version 2 (external)" {
		t.Errorf("external change was clobbered: %q", got)
	}
}

func TestWriteFile_NoChecksum_Overwrites(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "old")
	if _, err := e.writeFile(args("path", "a.txt", "content", "new")); err != nil {
		t.Fatalf("plain overwrite should succeed: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
}

func TestWriteFile_IfChecksum_DryRunAlsoRejected(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "current")
	stale := sha256Hex([]byte("stale"))
	_, err := e.writeFile(args("path", "a.txt", "content", "x", "if_checksum", stale, "dry_run", true))
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) || sErr.Code != ErrCodeStaleFile {
		t.Fatalf("expected dry_run to honor if_checksum, got: %v", err)
	}
}

func TestWriteFile_RejectsExistenceChangeAfterPreflight(t *testing.T) {
	t.Parallel()

	t.Run("missing target becomes empty file", func(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)
		path := filepath.Join(dir, "created-externally.txt")

		_, err := e.writeFileWithPreCommitHook(
			args("path", "created-externally.txt", "content", "agent content"),
			func() {
				if writeErr := os.WriteFile(path, nil, 0o640); writeErr != nil {
					t.Fatalf("external WriteFile: %v", writeErr)
				}
			},
		)
		requireStaleFileError(t, err)
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read external file: %v", readErr)
		}
		if len(got) != 0 {
			t.Fatalf("external empty file was overwritten: %q", got)
		}
	})

	t.Run("existing target disappears", func(t *testing.T) {
		t.Parallel()
		e, dir := testEngine(t)
		path := filepath.Join(dir, "removed-externally.txt")
		writeTestFile(t, dir, "removed-externally.txt", "before\n")

		_, err := e.writeFileWithPreCommitHook(
			args("path", "removed-externally.txt", "content", "agent content"),
			func() {
				if removeErr := os.Remove(path); removeErr != nil {
					t.Fatalf("external Remove: %v", removeErr)
				}
			},
		)
		requireStaleFileError(t, err)
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("externally removed file was recreated: stat error = %v", statErr)
		}
	})
}
