package jinn

import (
	"encoding/json"
	"testing"
)

// TestPush_MemoryPinStickinessAfterRefactor verifies that the memoryUpsertTx
// refactor preserves pin-stickiness: a second write without pin=true cannot clear
// a previously pinned key.
func TestPush_MemoryPinStickinessAfterRefactor(t *testing.T) {
	e, ctx := newPushEngine(t)

	// First push: pin the key.
	_, err := e.pushTool(ctx, map[string]interface{}{
		"memories": []interface{}{
			map[string]interface{}{"key": "sticky.key", "value": "v1", "scope": "global", "pin": true},
		},
	})
	if err != nil {
		t.Fatalf("first push (pin): %v", err)
	}

	// Second push: update value WITHOUT pin flag.
	_, err = e.pushTool(ctx, map[string]interface{}{
		"memories": []interface{}{
			map[string]interface{}{"key": "sticky.key", "value": "v2", "scope": "global"},
		},
	})
	if err != nil {
		t.Fatalf("second push (no pin): %v", err)
	}

	// Value must have updated.
	v, err := e.memoryTool(ctx, args("action", "recall", "key", "sticky.key", "scope", "global"))
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if v != "v2" {
		t.Errorf("value: got %q want v2", v)
	}

	// Pin must remain set.
	listRaw, err := e.memoryTool(ctx, args("action", "list", "scope", "global", "include_values", true))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var result struct {
		Entries []memoryEntry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(listRaw), &result); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var found bool
	for _, entry := range result.Entries {
		if entry.Key == "sticky.key" {
			found = true
			if !entry.Pinned {
				t.Error("expected sticky.key to remain pinned after write without pin flag")
			}
			break
		}
	}
	if !found {
		t.Error("sticky.key not found in memory list")
	}
}
