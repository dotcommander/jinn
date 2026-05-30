package jinn

import (
	"context"
	"testing"
)

// memory handler error-path coverage.
// These tests must NOT use t.Parallel because t.Setenv is process-wide.

func TestMemoryForget_InvalidKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", dir)
	e, _ := testEngine(t)

	_, err := e.memoryForget(context.Background(), args("key", "invalid/key"))
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestMemoryRecall_InvalidKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", dir)
	e, _ := testEngine(t)

	_, err := e.memoryRecall(context.Background(), args("key", "has space"))
	if err == nil {
		t.Fatal("expected error for invalid key in recall")
	}
}

func TestMemoryList_Empty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", dir)
	e, _ := testEngine(t)

	out, err := e.memoryList(context.Background(), args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty output for empty list")
	}
}
