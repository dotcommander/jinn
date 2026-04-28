package jinn

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSchema_Valid(t *testing.T) {
	t.Parallel()
	var tools []json.RawMessage
	if err := json.Unmarshal([]byte(Schema), &tools); err != nil {
		t.Fatalf("Schema is not valid JSON: %v", err)
	}
	if len(tools) != 14 {
		t.Fatalf("expected 14 tools, got %d", len(tools))
	}
}

func TestDispatch_UnknownTool(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, _, err := e.Dispatch(context.Background(), "nonexistent", args())
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

func TestDispatch_TextResult(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "hello.txt", "hello world\n")
	result, meta, err := e.Dispatch(context.Background(), "read_file", args("path", "hello.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != nil {
		t.Errorf("expected nil meta for read_file, got: %v", meta)
	}
	if result.Text == "" || !strings.Contains(result.Text, "hello world") {
		t.Errorf("expected text content, got: %s", result.Text)
	}
	if result.Content != nil {
		t.Errorf("expected nil Content for text file, got: %v", result.Content)
	}
	if result.Meta != nil {
		t.Errorf("expected nil Meta for small file, got: %v", result.Meta)
	}
}

func TestDispatch_TruncationMeta(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var content strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&content, "line%d\n", i)
	}
	writeTestFile(t, dir, "big.txt", content.String())
	result, _, err := e.Dispatch(context.Background(), "read_file", args("path", "big.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
}
