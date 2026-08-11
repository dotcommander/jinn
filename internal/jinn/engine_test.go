package jinn

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/dotcommander/jinn/internal/webfetch"
)

var expectedSchemaToolNames = []string{
	"run_plan", "run_shell", "read_file", "multi_read", "write_file",
	"edit_file", "multi_edit", "apply_patch", "search_files", "stat_file",
	"list_dir", "find_files", "list_tools", "detect_project", "memory",
	"undo", "lsp_query", "diff_files", "search_replace", "web_fetch", "web_search",
}

func TestSchema_Valid(t *testing.T) {
	t.Parallel()
	var tools []schemaTool
	if err := json.Unmarshal([]byte(Schema), &tools); err != nil {
		t.Fatalf("Schema is not valid JSON: %v", err)
	}
	if got := testSchemaToolNames(tools); !reflect.DeepEqual(got, expectedSchemaToolNames) {
		t.Fatalf("schema tools = %v, want %v", got, expectedSchemaToolNames)
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
	var tools []schemaTool
	if err := json.Unmarshal([]byte(schema), &tools); err != nil {
		t.Fatalf("compact schema is not valid JSON: %v", err)
	}
	if got := testSchemaToolNames(tools); !reflect.DeepEqual(got, expectedSchemaToolNames) {
		t.Fatalf("compact schema tools = %v, want %v", got, expectedSchemaToolNames)
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
	var tools []schemaTool
	if err := json.Unmarshal([]byte(schema), &tools); err != nil {
		t.Fatalf("lean schema is not valid JSON: %v", err)
	}
	if got := testSchemaToolNames(tools); !reflect.DeepEqual(got, expectedSchemaToolNames) {
		t.Fatalf("lean schema tools = %v, want %v", got, expectedSchemaToolNames)
	}
}

func testSchemaToolNames(tools []schemaTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Function.Name)
	}
	return names
}

func TestDispatch_UnknownTool(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, _, err := e.Dispatch(context.Background(), "nonexistent", args())
	if err == nil {
		t.Error("expected error for unknown tool")
	}
}

func TestNewSecureDefaultsAndExplicitUnsafeOptOut(t *testing.T) {
	dir := t.TempDir()
	secure := New(dir, "test")
	t.Cleanup(func() { _ = secure.Close() })
	if secure.ShellMode() != ShellModeDisabled || !secure.requireMutationPreconditions {
		t.Fatalf("New security defaults: mode=%q preconditions=%v", secure.ShellMode(), secure.requireMutationPreconditions)
	}
	unsafe, err := NewWithConfig(t.TempDir(), EngineConfig{
		Version: "test", ShellMode: ShellModeUnsafe,
		UnsafeAllowMutationWithoutPreconditions: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unsafe.Close() })
	if unsafe.requireMutationPreconditions {
		t.Fatal("explicit unsafe opt-out did not disable mutation preconditions")
	}
}

func TestNewWithConfigDefaultWebUserAgent(t *testing.T) {
	engine, err := NewWithConfig(t.TempDir(), EngineConfig{Version: "v1.2.3", ShellMode: ShellModeDisabled})
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	if engine.webConfig.UserAgent != "jinn/v1.2.3" {
		t.Fatalf("default web user agent = %q", engine.webConfig.UserAgent)
	}

	dev, err := NewWithConfig(t.TempDir(), EngineConfig{ShellMode: ShellModeDisabled})
	if err != nil {
		t.Fatalf("NewWithConfig dev: %v", err)
	}
	t.Cleanup(func() { _ = dev.Close() })
	if dev.webConfig.UserAgent != "jinn/dev" {
		t.Fatalf("blank-version web user agent = %q", dev.webConfig.UserAgent)
	}

	custom, err := NewWithConfig(t.TempDir(), EngineConfig{Version: "v1.2.3", ShellMode: ShellModeDisabled, Web: webfetch.Config{UserAgent: "custom-agent"}})
	if err != nil {
		t.Fatalf("NewWithConfig custom: %v", err)
	}
	t.Cleanup(func() { _ = custom.Close() })
	if custom.webConfig.UserAgent != "custom-agent" {
		t.Fatalf("custom web user agent = %q", custom.webConfig.UserAgent)
	}
}

func TestModeSpecificDiscoveryExcludesNestedShellOperations(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode ShellMode
		want int
	}{{ShellModeDisabled, 20}, {ShellModeUnsafe, 21}} {
		raw, err := LeanSchemaForMode(tc.mode)
		if err != nil {
			t.Fatal(err)
		}
		var schema []any
		if err := json.Unmarshal([]byte(raw), &schema); err != nil {
			t.Fatal(err)
		}
		if len(schema) != tc.want {
			t.Errorf("mode %s schema count = %d, want %d", tc.mode, len(schema), tc.want)
		}
		if tc.mode == ShellModeDisabled && strings.Contains(raw, `"shell":{"type"`) {
			t.Fatal("disabled schema retains nested shell operation property")
		}
	}
	resp, err := RouteTools(RouteRequest{Need: "run a shell command", MaxTools: RouteMaxTools})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := routeMatchNamed(resp.Matches, "run_shell"); ok {
		t.Fatal("secure RouteTools recommended run_shell")
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
