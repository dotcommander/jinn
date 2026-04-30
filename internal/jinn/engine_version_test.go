package jinn

import (
	"context"
	"testing"
)

func TestResolveVersion_NonDev(t *testing.T) {
	t.Parallel()
	// Non-"dev" value passes through unchanged.
	got := ResolveVersion("v1.2.3")
	if got != "v1.2.3" {
		t.Errorf("got %q, want %q", got, "v1.2.3")
	}
}

func TestResolveVersion_DevFallsBackToSomething(t *testing.T) {
	t.Parallel()
	// "dev" triggers VCS lookup; result may be a hash, module version, or "dev"
	// — but must never be empty.
	got := ResolveVersion("dev")
	if got == "" {
		t.Error("ResolveVersion('dev') returned empty string")
	}
}

func TestDispatch_ListTools(t *testing.T) {
	t.Parallel()
	// list_tools is a zero-arg tool that always succeeds.
	// Exercises the Dispatch → list_tools branch (previously uncovered).
	e, _ := testEngine(t)
	result, meta, err := e.Dispatch(context.Background(), "list_tools", args())
	if err != nil {
		t.Fatalf("list_tools: %v", err)
	}
	if meta != nil {
		t.Errorf("list_tools should return nil meta, got: %v", meta)
	}
	if result.Text == "" {
		t.Error("list_tools result should not be empty")
	}
}

func TestDispatch_StatFile(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "x.txt", "content")
	result, _, err := e.Dispatch(context.Background(), "stat_file", args("path", "x.txt"))
	if err != nil {
		t.Fatalf("stat_file: %v", err)
	}
	if result.Text == "" {
		t.Error("stat_file result should not be empty")
	}
}

func TestDispatch_FindFiles(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "hello.go", "package main")
	result, _, err := e.Dispatch(context.Background(), "find_files", args("pattern", "*.go"))
	if err != nil {
		t.Fatalf("find_files: %v", err)
	}
	if result.Text == "" {
		t.Error("find_files result should not be empty")
	}
}

func TestDispatch_ChecksumTree(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "data")
	result, _, err := e.Dispatch(context.Background(), "checksum_tree", args())
	if err != nil {
		t.Fatalf("checksum_tree: %v", err)
	}
	if result.Text == "" {
		t.Error("checksum_tree result should not be empty")
	}
}

func TestDispatch_DetectProject(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "go.mod", "module example.com/test\n\ngo 1.21\n")
	result, _, err := e.Dispatch(context.Background(), "detect_project", args())
	if err != nil {
		t.Fatalf("detect_project: %v", err)
	}
	if result.Text == "" {
		t.Error("detect_project result should not be empty")
	}
}

func TestDispatch_WriteFile(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	result, _, err := e.Dispatch(context.Background(), "write_file", args("path", "out.txt", "content", "hi"))
	if err != nil {
		t.Fatalf("write_file: %v", err)
	}
	if result.Text == "" {
		t.Error("write_file result should not be empty")
	}
}

func TestDispatch_SearchFiles(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "needle.txt", "the needle is here")
	result, _, err := e.Dispatch(context.Background(), "search_files", args("pattern", "needle", "path", "."))
	if err != nil {
		t.Fatalf("search_files: %v", err)
	}
	if result.Text == "" {
		t.Error("search_files result should not be empty")
	}
}
