package jinn

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Memory tests are NOT parallel: t.Setenv modifies the process environment and
// is incompatible with t.Parallel() — Go panics when both are used together.
// Each test sets JINN_CONFIG_DIR to an isolated temp dir so tests never touch
// ~/.config/jinn/memory.json.

func memCfgDir(t *testing.T) {
	t.Helper()
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
}

func TestMemory_SaveRecallRoundtrip(t *testing.T) {
	memCfgDir(t)
	e, _ := testEngine(t)

	out, err := e.memoryTool(args("action", "save", "key", "greeting", "value", "hello world"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if out != "saved: greeting" {
		t.Errorf("save output: %q", out)
	}

	out, err = e.memoryTool(args("action", "recall", "key", "greeting"))
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if out != "hello world" {
		t.Errorf("recall value: %q", out)
	}
}

func TestMemory_RecallMissing_ErrWithSuggestion(t *testing.T) {
	memCfgDir(t)
	e, _ := testEngine(t)

	_, err := e.memoryTool(args("action", "recall", "key", "no-such-key"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "key not found") {
		t.Errorf("error text: %q", err.Error())
	}
	var ews *ErrWithSuggestion
	if !errors.As(err, &ews) {
		t.Fatalf("expected *ErrWithSuggestion, got %T", err)
	}
	if !strings.Contains(ews.Suggestion, "list") {
		t.Errorf("suggestion should mention 'list': %q", ews.Suggestion)
	}
}

func TestMemory_ListSortedAlphabetically(t *testing.T) {
	memCfgDir(t)
	e, _ := testEngine(t)

	for _, k := range []string{"zebra", "apple", "mango"} {
		if _, err := e.memoryTool(args("action", "save", "key", k, "value", k+"-val")); err != nil {
			t.Fatalf("save %s: %v", k, err)
		}
	}

	out, err := e.memoryTool(args("action", "list"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var res struct {
		Keys  []string `json:"keys"`
		Count int      `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if res.Count != 3 {
		t.Errorf("count: %d", res.Count)
	}
	if len(res.Keys) != 3 {
		t.Fatalf("keys len: %d", len(res.Keys))
	}
	if res.Keys[0] != "apple" || res.Keys[1] != "mango" || res.Keys[2] != "zebra" {
		t.Errorf("order: %v", res.Keys)
	}
}

func TestMemory_ListEmpty(t *testing.T) {
	memCfgDir(t)
	e, _ := testEngine(t)

	out, err := e.memoryTool(args("action", "list"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var res struct {
		Keys  []string `json:"keys"`
		Count int      `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res.Count != 0 || len(res.Keys) != 0 {
		t.Errorf("expected empty list, got: %+v", res)
	}
}

func TestMemory_ForgetIdempotent(t *testing.T) {
	memCfgDir(t)
	e, _ := testEngine(t)

	// Save then forget.
	if _, err := e.memoryTool(args("action", "save", "key", "temp", "value", "x")); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := e.memoryTool(args("action", "forget", "key", "temp"))
	if err != nil {
		t.Fatalf("forget existing: %v", err)
	}
	if out != "forgotten: temp" {
		t.Errorf("forget output: %q", out)
	}

	// Forget again — idempotent, no error.
	out, err = e.memoryTool(args("action", "forget", "key", "temp"))
	if err != nil {
		t.Fatalf("forget missing: %v", err)
	}
	if out != "forgotten: temp" {
		t.Errorf("forget-missing output: %q", out)
	}

	// Confirm gone from list.
	listOut, err := e.memoryTool(args("action", "list"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(listOut, "temp") {
		t.Errorf("key still present after forget: %s", listOut)
	}
}

func TestMemory_UnknownAction(t *testing.T) {
	memCfgDir(t)
	e, _ := testEngine(t)

	_, err := e.memoryTool(args("action", "explode"))
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	var ews *ErrWithSuggestion
	if !errors.As(err, &ews) {
		t.Fatalf("expected *ErrWithSuggestion, got %T", err)
	}
	if !strings.Contains(ews.Suggestion, "save") || !strings.Contains(ews.Suggestion, "forget") {
		t.Errorf("suggestion should list valid actions: %q", ews.Suggestion)
	}
}

func TestMemory_InvalidKey(t *testing.T) {
	memCfgDir(t)
	e, _ := testEngine(t)

	cases := []string{"", "has space", "has/slash", strings.Repeat("x", 129)}
	for _, k := range cases {
		_, err := e.memoryTool(args("action", "save", "key", k, "value", "v"))
		if err == nil {
			t.Errorf("expected error for key %q", k)
			continue
		}
		var ews *ErrWithSuggestion
		if !errors.As(err, &ews) {
			t.Errorf("key %q: expected *ErrWithSuggestion, got %T", k, err)
		}
	}
}

func TestMemory_OversizeValue(t *testing.T) {
	memCfgDir(t)
	e, _ := testEngine(t)

	bigVal := strings.Repeat("x", memoryMaxValueBytes+1)
	_, err := e.memoryTool(args("action", "save", "key", "big", "value", bigVal))
	if err == nil {
		t.Fatal("expected error for oversize value")
	}
	var ews *ErrWithSuggestion
	if !errors.As(err, &ews) {
		t.Fatalf("expected *ErrWithSuggestion, got %T", err)
	}
	if !strings.Contains(ews.Suggestion, "trim") {
		t.Errorf("suggestion should mention trimming: %q", ews.Suggestion)
	}
}

func TestMemory_AtomicWriteMultipleEntries(t *testing.T) {
	memCfgDir(t)
	e, _ := testEngine(t)

	// Write N entries and verify all survive a full reload (loadMemory reads
	// the file on every call, so each recall exercises the full persist path).
	keys := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	for _, k := range keys {
		if _, err := e.memoryTool(args("action", "save", "key", k, "value", "val-"+k)); err != nil {
			t.Fatalf("save %s: %v", k, err)
		}
	}

	for _, k := range keys {
		out, err := e.memoryTool(args("action", "recall", "key", k))
		if err != nil {
			t.Fatalf("recall %s: %v", k, err)
		}
		if out != "val-"+k {
			t.Errorf("key %s: got %q", k, out)
		}
	}

	listOut, err := e.memoryTool(args("action", "list"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var res struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(listOut), &res); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if res.Count != len(keys) {
		t.Errorf("count: got %d, want %d", res.Count, len(keys))
	}
}

func TestMemory_UpsertOverwrites(t *testing.T) {
	memCfgDir(t)
	e, _ := testEngine(t)

	if _, err := e.memoryTool(args("action", "save", "key", "k", "value", "first")); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if _, err := e.memoryTool(args("action", "save", "key", "k", "value", "second")); err != nil {
		t.Fatalf("second save: %v", err)
	}
	out, err := e.memoryTool(args("action", "recall", "key", "k"))
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if out != "second" {
		t.Errorf("upsert: got %q, want %q", out, "second")
	}
}
