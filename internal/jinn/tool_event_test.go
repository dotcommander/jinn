package jinn

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func decodeEvents(t *testing.T, s string) []Event {
	t.Helper()
	var events []Event
	if err := json.Unmarshal([]byte(s), &events); err != nil {
		t.Fatalf("decode events: %v\nraw: %s", err, s)
	}
	return events
}

// newEventEngine returns a fresh engine for event tests.
func newEventEngine(t *testing.T) (*Engine, context.Context) {
	t.Helper()
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	e := New(t.TempDir(), "dev")
	t.Cleanup(func() { _ = e.Close() })
	return e, context.Background()
}

// TestEvent_AppendAndList verifies basic append + list round-trip.
func TestEvent_AppendAndList(t *testing.T) {
	e, ctx := newEventEngine(t)

	r, err := e.eventTool(ctx, args("action", "append", "kind", "progress", "message", "hello world", "agent", "tester"))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if !strings.Contains(r, `"id"`) {
		t.Errorf("append result missing id: %s", r)
	}

	lr, err := e.eventTool(ctx, args("action", "list"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	evts := decodeEvents(t, lr)
	if len(evts) == 0 {
		t.Fatal("expected at least 1 event")
	}
	found := false
	for _, ev := range evts {
		if ev.Kind == "progress" && ev.Message == "hello world" {
			found = true
		}
	}
	if !found {
		t.Errorf("appended event not found in list: %s", lr)
	}
}

// TestEvent_OversizeMessageRejected verifies message > 4096 chars is rejected.
func TestEvent_OversizeMessageRejected(t *testing.T) {
	e, ctx := newEventEngine(t)

	big := strings.Repeat("x", maxEventMessage+1)
	_, err := e.eventTool(ctx, args("action", "append", "kind", "test", "message", big))
	if err == nil {
		t.Fatal("want error for oversize message, got nil")
	}
	var ews *ErrWithSuggestion
	if !isErrWithSuggestion(err, &ews) || ews.Code != ErrCodeInvalidArgs {
		t.Errorf("want ErrCodeInvalidArgs, got %v", err)
	}
}

// TestEvent_OversizeMetadataRejected verifies metadata > 16384 bytes is rejected.
func TestEvent_OversizeMetadataRejected(t *testing.T) {
	e, ctx := newEventEngine(t)

	big := `{"x":"` + strings.Repeat("y", maxEventMetadata) + `"}`
	_, err := e.eventTool(ctx, args("action", "append", "kind", "test", "message", "msg", "metadata", big))
	if err == nil {
		t.Fatal("want error for oversize metadata, got nil")
	}
}

// TestEvent_InvalidMetadataJSONRejected verifies non-JSON metadata string is rejected.
func TestEvent_InvalidMetadataJSONRejected(t *testing.T) {
	e, ctx := newEventEngine(t)

	_, err := e.eventTool(ctx, args("action", "append", "kind", "test", "message", "msg", "metadata", "not-json"))
	if err == nil {
		t.Fatal("want error for invalid metadata JSON, got nil")
	}
	var ews *ErrWithSuggestion
	if !isErrWithSuggestion(err, &ews) || ews.Code != ErrCodeInvalidArgs {
		t.Errorf("want ErrCodeInvalidArgs, got %v", err)
	}
}

// TestEvent_ValidObjectMetadataAccepted verifies JSON object arg is accepted and stored.
func TestEvent_ValidObjectMetadataAccepted(t *testing.T) {
	e, ctx := newEventEngine(t)

	meta := map[string]interface{}{"key": "value", "n": float64(42)}
	r, err := e.eventTool(ctx, args("action", "append", "kind", "test", "message", "with meta", "metadata", meta))
	if err != nil {
		t.Fatalf("append with object metadata: %v", err)
	}
	if !strings.Contains(r, "metadata") {
		t.Errorf("response missing metadata field: %s", r)
	}
}

// TestEvent_ValidStringMetadataAccepted verifies a JSON string is accepted as metadata.
func TestEvent_ValidStringMetadataAccepted(t *testing.T) {
	e, ctx := newEventEngine(t)

	_, err := e.eventTool(ctx, args("action", "append", "kind", "test", "message", "str meta", "metadata", `{"a":1}`))
	if err != nil {
		t.Fatalf("append with string metadata: %v", err)
	}
}

// TestEvent_ListFilterByTaskID verifies task_id filter.
func TestEvent_ListFilterByTaskID(t *testing.T) {
	e, ctx := newEventEngine(t)

	e.eventTool(ctx, args("action", "append", "kind", "a", "message", "for task A", "task_id", "task_aaa")) //nolint:errcheck
	e.eventTool(ctx, args("action", "append", "kind", "b", "message", "for task B", "task_id", "task_bbb")) //nolint:errcheck

	lr, err := e.eventTool(ctx, args("action", "list", "task_id", "task_aaa"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	evts := decodeEvents(t, lr)
	for _, ev := range evts {
		if ev.TaskID != "task_aaa" {
			t.Errorf("filter leaked task_id %q", ev.TaskID)
		}
	}
	if len(evts) == 0 {
		t.Error("expected at least 1 event for task_aaa")
	}
}

// TestEvent_ListFilterByKind verifies kind filter.
func TestEvent_ListFilterByKind(t *testing.T) {
	e, ctx := newEventEngine(t)

	e.eventTool(ctx, args("action", "append", "kind", "foo", "message", "msg1")) //nolint:errcheck
	e.eventTool(ctx, args("action", "append", "kind", "bar", "message", "msg2")) //nolint:errcheck

	lr, err := e.eventTool(ctx, args("action", "list", "kind", "foo"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	evts := decodeEvents(t, lr)
	for _, ev := range evts {
		if ev.Kind != "foo" {
			t.Errorf("kind filter leaked kind %q", ev.Kind)
		}
	}
}

// TestEvent_LimitCap verifies requesting 500 returns ≤100.
func TestEvent_LimitCap(t *testing.T) {
	e, ctx := newEventEngine(t)

	// Insert 110 events.
	for i := 0; i < 110; i++ {
		e.eventTool(ctx, args("action", "append", "kind", "fill", "message", "m")) //nolint:errcheck
	}

	lr, err := e.eventTool(ctx, args("action", "list", "limit", float64(500)))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	evts := decodeEvents(t, lr)
	if len(evts) > 100 {
		t.Errorf("limit cap: want ≤100 but got %d", len(evts))
	}
}

// TestEvent_SinceIDPaging verifies since_id returns only newer events.
func TestEvent_SinceIDPaging(t *testing.T) {
	e, ctx := newEventEngine(t)

	e.eventTool(ctx, args("action", "append", "kind", "page", "message", "first"))  //nolint:errcheck
	e.eventTool(ctx, args("action", "append", "kind", "page", "message", "second")) //nolint:errcheck
	e.eventTool(ctx, args("action", "append", "kind", "page", "message", "third"))  //nolint:errcheck

	// Get all to find first event's id.
	allR, _ := e.eventTool(ctx, args("action", "list", "kind", "page"))
	all := decodeEvents(t, allR)
	if len(all) < 3 {
		t.Fatalf("expected 3 events, got %d", len(all))
	}
	pivot := all[0].ID

	// since_id=pivot should return events after pivot.
	lr, err := e.eventTool(ctx, args("action", "list", "kind", "page", "since_id", float64(pivot)))
	if err != nil {
		t.Fatalf("list since_id: %v", err)
	}
	paged := decodeEvents(t, lr)
	for _, ev := range paged {
		if ev.ID <= pivot {
			t.Errorf("since_id=%d: got event with id=%d (not greater)", pivot, ev.ID)
		}
	}
	if len(paged) != 2 {
		t.Errorf("want 2 events after pivot, got %d", len(paged))
	}
}

// TestEvent_InvalidAction verifies unknown actions are rejected.
func TestEvent_InvalidAction(t *testing.T) {
	e, ctx := newEventEngine(t)
	_, err := e.eventTool(ctx, args("action", "destroy"))
	if err == nil {
		t.Fatal("want error for unknown action, got nil")
	}
}

// isErrWithSuggestion checks whether err (possibly wrapped) is *ErrWithSuggestion.
func isErrWithSuggestion(err error, out **ErrWithSuggestion) bool {
	if e, ok := err.(*ErrWithSuggestion); ok {
		*out = e
		return true
	}
	return false
}
