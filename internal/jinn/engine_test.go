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
	if len(tools) != 21 {
		t.Fatalf("expected 21 tools, got %d", len(tools))
	}
}

func TestCompactSchema_Valid(t *testing.T) {
	t.Parallel()
	schema, err := CompactSchema()
	if err != nil {
		t.Fatalf("CompactSchema: %v", err)
	}
	if strings.Contains(schema, "\n") {
		t.Fatal("compact schema should not contain newlines")
	}
	var tools []json.RawMessage
	if err := json.Unmarshal([]byte(schema), &tools); err != nil {
		t.Fatalf("compact schema is not valid JSON: %v", err)
	}
	if len(tools) != 21 {
		t.Fatalf("expected 21 tools, got %d", len(tools))
	}
}

func TestLeanSchema_Valid(t *testing.T) {
	t.Parallel()
	schema, err := LeanSchema()
	if err != nil {
		t.Fatalf("LeanSchema: %v", err)
	}
	if strings.Contains(schema, "file path to read") {
		t.Fatal("lean schema should omit nested parameter descriptions")
	}
	if !strings.Contains(schema, "Read file contents") {
		t.Fatal("lean schema should keep function descriptions")
	}
	var tools []json.RawMessage
	if err := json.Unmarshal([]byte(schema), &tools); err != nil {
		t.Fatalf("lean schema is not valid JSON: %v", err)
	}
	if len(tools) != 21 {
		t.Fatalf("expected 21 tools, got %d", len(tools))
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
	// 300 lines fits in 2000-line default window → no truncation
	if result.Meta != nil {
		t.Errorf("expected nil Meta for file that fits in default window, got: %v", result.Meta)
	}
}
