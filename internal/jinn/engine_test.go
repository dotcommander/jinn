package jinn

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSchema_Valid(t *testing.T) {
	t.Parallel()
	var tools []json.RawMessage
	if err := json.Unmarshal([]byte(Schema), &tools); err != nil {
		t.Fatalf("Schema is not valid JSON: %v", err)
	}
	if len(tools) != 8 {
		t.Fatalf("expected 8 tools, got %d", len(tools))
	}
}

func TestDispatch_UnknownTool(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.Dispatch(context.Background(), "nonexistent", args())
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}
