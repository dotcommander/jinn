package jinn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChecksumTree_Basic(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "hello")
	writeTestFile(t, dir, "b.txt", "world")

	result, err := e.checksumTree(args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var hashes map[string]string
	if err := json.Unmarshal([]byte(result), &hashes); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(hashes) != 2 {
		t.Errorf("expected 2 entries, got %d", len(hashes))
	}

	// Verify hash is correct SHA-256
	h := sha256.Sum256([]byte("hello"))
	expected := hex.EncodeToString(h[:])
	if hashes["a.txt"] != expected {
		t.Errorf("hash for a.txt = %q, want %q", hashes["a.txt"], expected)
	}
}

func TestChecksumTree_SkipsIgnoredDirs(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "hello")
	secret := filepath.Join(dir, ".git", "objects", "pack", "data")
	os.MkdirAll(filepath.Dir(secret), 0755)
	os.WriteFile(secret, []byte("secret"), 0644)

	result, err := e.checksumTree(args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var hashes map[string]string
	json.Unmarshal([]byte(result), &hashes)
	for k := range hashes {
		if strings.Contains(k, ".git") {
			t.Errorf("should not include .git files, got: %s", k)
		}
	}
}

func TestChecksumTree_WithPattern(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.go", "package main")
	writeTestFile(t, dir, "b.txt", "hello")

	result, err := e.checksumTree(args("pattern", "*.go"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var hashes map[string]string
	json.Unmarshal([]byte(result), &hashes)
	if len(hashes) != 1 {
		t.Errorf("expected 1 entry with *.go filter, got %d", len(hashes))
	}
	if _, ok := hashes["a.go"]; !ok {
		t.Error("expected a.go in results")
	}
}

func TestChecksumTree_NonexistentPath(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.checksumTree(args("path", "nope"))
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestChecksumTree_EmptyDirectory(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	sub := filepath.Join(dir, "empty")
	os.MkdirAll(sub, 0755)

	result, err := e.checksumTree(args("path", "empty"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "{}" {
		t.Errorf("expected {}, got %q", result)
	}
}

func TestChecksumTree_Deterministic(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "x.txt", "content")

	r1, _ := e.checksumTree(args())
	r2, _ := e.checksumTree(args())
	if r1 != r2 {
		t.Errorf("hashes should be deterministic:\n%s\n%s", r1, r2)
	}
}
