package mcpbatch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/voocel/mcp-sdk-go/protocol"
)

func TestDecodeStrictLimitsAndNumbers(t *testing.T) {
	input, err := Decode(strings.NewReader(`{"version":1,"calls":[{"id":"one","tool":"read","arguments":{"n":9007199254740993}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := input.Calls[0].Arguments["n"]; got != json.Number("9007199254740993") {
		t.Fatalf("number = %#v", got)
	}
	for _, raw := range []string{
		`{"version":1,"unknown":true,"calls":[{"id":"one","tool":"read"}]}`,
		`{"version":1,"calls":[{"id":"one","tool":"read","unknown":true}]}`,
		`{"version":1,"calls":[{"id":"one","tool":"read"}]} {}`,
		`{"version":1,"max_concurrency":0,"calls":[{"id":"one","tool":"read"}]}`,
		`{"version":1,"calls":[]}`,
	} {
		if _, err := Decode(strings.NewReader(raw)); err == nil {
			t.Fatalf("Decode(%s) succeeded", raw)
		}
	}
	if _, err := Decode(strings.NewReader(strings.Repeat("x", maxInputBytes+1))); err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") {
		t.Fatalf("oversized input error = %v", err)
	}
}

func TestValidateAnnotationsSchemasAndNoExternalRefs(t *testing.T) {
	readOnly := true
	readOnlyFalse := false
	destructive := true
	destructiveFalse := false
	approved := []*protocol.Tool{{Name: "read", InputSchema: protocol.JSONSchema{"type": "object", "properties": map[string]any{"count": map[string]any{"type": "integer"}}, "required": []any{"count"}, "additionalProperties": false}, Annotations: &protocol.ToolAnnotations{ReadOnlyHint: &readOnly}}}
	input := batchInput(Call{ID: "one", Tool: "read", Arguments: map[string]any{"count": json.Number("9007199254740993")}})
	if err := Validate(input, approved, approved); err != nil {
		t.Fatalf("Validate exact integer: %v", err)
	}
	if err := Validate(batchInput(Call{ID: "one", Tool: "read", Arguments: map[string]any{"count": json.Number("1.5")}}), approved, approved); err == nil {
		t.Fatal("fractional integer accepted")
	}
	for _, test := range []struct {
		name     string
		approved *protocol.Tool
		current  *protocol.Tool
		valid    bool
	}{
		{name: "approved missing", approved: batchTool(nil, nil), current: approved[0]},
		{name: "approved false", approved: batchTool(&readOnlyFalse, nil), current: approved[0]},
		{name: "current missing", approved: approved[0], current: batchTool(nil, nil)},
		{name: "current false", approved: approved[0], current: batchTool(&readOnlyFalse, nil)},
		{name: "destructive nil", approved: batchTool(&readOnly, nil), current: batchTool(&readOnly, nil), valid: true},
		{name: "destructive false", approved: batchTool(&readOnly, &destructiveFalse), current: batchTool(&readOnly, &destructiveFalse), valid: true},
		{name: "approved destructive", approved: batchTool(&readOnly, &destructive), current: approved[0]},
		{name: "current destructive", approved: approved[0], current: batchTool(&readOnly, &destructive)},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(input, []*protocol.Tool{test.approved}, []*protocol.Tool{test.current})
			if test.valid && err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if !test.valid && (err == nil || !strings.Contains(err.Error(), "readOnlyHint")) {
				t.Fatalf("unsafe annotation error = %v", err)
			}
		})
	}
	external := []*protocol.Tool{{Name: "read", InputSchema: protocol.JSONSchema{"$ref": "https://example.test/schema"}, Annotations: &protocol.ToolAnnotations{ReadOnlyHint: &readOnly}}}
	if err := Validate(batchInput(Call{ID: "one", Tool: "read"}), external, external); err == nil || !strings.Contains(err.Error(), "external JSON Schema reference") {
		t.Fatalf("external reference error = %v", err)
	}
}

func TestValidatePreflightIsAtomic(t *testing.T) {
	readOnly := true
	tool := batchTool(&readOnly, nil)
	input := batchInput(
		Call{ID: "valid", Tool: "read", Arguments: map[string]any{"count": json.Number("1")}},
		Call{ID: "invalid", Tool: "read", Arguments: map[string]any{"count": json.Number("1.5")}},
	)
	caller := &testCaller{}
	if err := Validate(input, []*protocol.Tool{tool}, []*protocol.Tool{tool}); err == nil {
		t.Fatal("invalid batch passed preflight")
	}
	if caller.callCount() != 0 {
		t.Fatalf("preflight invoked %d tools", caller.callCount())
	}
}

func TestExecuteOrderConcurrencyErrorsAndCancellation(t *testing.T) {
	input := batchInput(Call{ID: "slow", Tool: "slow"}, Call{ID: "fast", Tool: "fast"})
	concurrency := 2
	input.MaxConcurrency = &concurrency
	caller := &testCaller{delays: map[string]time.Duration{"slow": 20 * time.Millisecond, "fast": time.Millisecond}}
	results := Execute(context.Background(), caller, input, testProjector)
	if !AllOK(results) || results[0].ID != "slow" || results[1].ID != "fast" || caller.maxActive > 2 {
		t.Fatalf("ordered results = %#v, max active=%d", results, caller.maxActive)
	}

	failFast := batchInput(Call{ID: "bad", Tool: "bad"}, Call{ID: "later", Tool: "later"})
	failFast.FailFast = true
	one := 1
	failFast.MaxConcurrency = &one
	results = Execute(context.Background(), &testCaller{toolErrors: map[string]bool{"bad": true}}, failFast, testProjector)
	if results[0].Status != statusToolError || results[0].Result.(string) != "normalized:bad" || results[1].Status != statusSkipped {
		t.Fatalf("fail-fast results = %#v", results)
	}

	timedOut := batchInput(Call{ID: "wait", Tool: "wait"})
	timedOut.CallTimeout = time.Millisecond
	results = Execute(context.Background(), &testCaller{waitForContext: true}, timedOut, testProjector)
	if results[0].Status != statusCanceled {
		t.Fatalf("timeout result = %#v", results[0])
	}

	transport := batchInput(Call{ID: "broken", Tool: "broken"})
	results = Execute(context.Background(), &testCaller{callErr: errors.New("broken transport")}, transport, testProjector)
	if results[0].Status != statusTransportError || results[0].Error != "broken transport" {
		t.Fatalf("transport result = %#v", results[0])
	}
}

func TestExecuteFailFastCancelsStartedCallAndOverallTimeoutCleansUp(t *testing.T) {
	input := batchInput(Call{ID: "bad", Tool: "bad"}, Call{ID: "wait", Tool: "wait"}, Call{ID: "pending", Tool: "pending"})
	input.FailFast = true
	two := 2
	input.MaxConcurrency = &two
	caller := &testCaller{delays: map[string]time.Duration{"bad": 25 * time.Millisecond}, toolErrors: map[string]bool{"bad": true}, waitTools: map[string]bool{"wait": true}}
	results := Execute(context.Background(), caller, input, testProjector)
	if results[0].Status != statusToolError || results[1].Status != statusCanceled || results[2].Status != statusSkipped || caller.callCount() != 2 {
		t.Fatalf("fail-fast cancellation = %#v, calls=%d", results, caller.callCount())
	}

	timedOut := batchInput(Call{ID: "wait", Tool: "wait"})
	parent, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	caller = &testCaller{waitTools: map[string]bool{"wait": true}}
	results = Execute(parent, caller, timedOut, testProjector)
	if results[0].Status != statusCanceled || caller.callCount() != 1 {
		t.Fatalf("overall timeout cleanup = %#v, calls=%d", results, caller.callCount())
	}
}

func batchInput(calls ...Call) Input {
	concurrency := 1
	return Input{Version: 1, Calls: calls, MaxConcurrency: &concurrency, CallTimeout: time.Second}
}

func testProjector(result *protocol.CallToolResult, call Call) (any, error) {
	return "normalized:" + call.Tool, nil
}

func batchTool(readOnly, destructive *bool) *protocol.Tool {
	return &protocol.Tool{Name: "read", InputSchema: protocol.JSONSchema{"type": "object", "properties": map[string]any{"count": map[string]any{"type": "integer"}}, "required": []any{"count"}, "additionalProperties": false}, Annotations: &protocol.ToolAnnotations{ReadOnlyHint: readOnly, DestructiveHint: destructive}}
}

type testCaller struct {
	mu             sync.Mutex
	active         int
	maxActive      int
	delays         map[string]time.Duration
	toolErrors     map[string]bool
	callErr        error
	waitForContext bool
	waitTools      map[string]bool
	calls          int
}

func (caller *testCaller) Call(ctx context.Context, name string, _ map[string]any) (*protocol.CallToolResult, error) {
	caller.mu.Lock()
	caller.calls++
	caller.active++
	if caller.active > caller.maxActive {
		caller.maxActive = caller.active
	}
	caller.mu.Unlock()
	defer func() {
		caller.mu.Lock()
		caller.active--
		caller.mu.Unlock()
	}()
	if caller.waitForContext || caller.waitTools[name] {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if delay := caller.delays[name]; delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if caller.callErr != nil {
		return nil, caller.callErr
	}
	if caller.toolErrors[name] {
		return &protocol.CallToolResult{IsError: true}, nil
	}
	return &protocol.CallToolResult{}, nil
}

func (caller *testCaller) callCount() int {
	caller.mu.Lock()
	defer caller.mu.Unlock()
	return caller.calls
}
