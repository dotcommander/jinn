package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestMCPExplorerCallProjectionRFC6901(t *testing.T) {
	output := mcpExplorerProjectionTestOutput()

	t.Run("root", func(t *testing.T) {
		got := renderMCPExplorerProjection(t, output, mcpExplorerProjectionOptions{pointer: "", hasSelect: true})
		if got.SchemaVersion != 1 || got.Projection.Pointer != "" {
			t.Fatalf("root projection = %+v", got)
		}
		value, ok := got.Value.(map[string]any)
		if !ok || value["resultType"] != "complete" {
			t.Fatalf("root value = %#v", got.Value)
		}
	})

	t.Run("object and array", func(t *testing.T) {
		got := renderMCPExplorerProjection(t, output, mcpExplorerProjectionOptions{pointer: "/structuredContent/items/1/name", hasSelect: true})
		if got.Value != "second" || got.Projection.TotalItems != nil || got.Projection.ReturnedItems != nil || got.Projection.Truncated {
			t.Fatalf("object/array projection = %+v", got)
		}
	})

	t.Run("escaped tokens", func(t *testing.T) {
		got := renderMCPExplorerProjection(t, output, mcpExplorerProjectionOptions{pointer: "/structuredContent/a~1b/~0key", hasSelect: true})
		if got.Value != "escaped" {
			t.Fatalf("escaped projection value = %#v", got.Value)
		}
	})
}

func TestMCPExplorerCallProjectionPointerErrorsAndNull(t *testing.T) {
	output := mcpExplorerProjectionTestOutput()
	tests := []struct {
		name    string
		pointer string
		want    string
	}{
		{name: "invalid prefix", pointer: "structuredContent", want: `invalid --select pointer "structuredContent": must be empty or start with '/'`},
		{name: "bad escape", pointer: "/structuredContent/a~2b", want: `invalid --select pointer "/structuredContent/a~2b": invalid escape in "a~2b"`},
		{name: "missing key", pointer: "/structuredContent/missing", want: `--select pointer "/structuredContent/missing": path not found at "missing"`},
		{name: "leading zero index", pointer: "/structuredContent/items/01", want: `--select pointer "/structuredContent/items/01": invalid array index "01"`},
		{name: "non-numeric index", pointer: "/structuredContent/items/-", want: `--select pointer "/structuredContent/items/-": invalid array index "-"`},
		{name: "out of range", pointer: "/structuredContent/items/2", want: `--select pointer "/structuredContent/items/2": array index "2" is out of range`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := selectMCPExplorerCallOutput(output, test.pointer)
			if err == nil || err.Error() != test.want {
				t.Fatalf("selectMCPExplorerCallOutput(%q) error = %v, want %q", test.pointer, err, test.want)
			}
			var projectionErr *mcpExplorerProjectionError
			if !strings.Contains(err.Error(), "--select") || !errors.As(err, &projectionErr) || projectionErr.suggestion == "" {
				t.Fatalf("projection error = %#v", err)
			}
		})
	}

	got := renderMCPExplorerProjection(t, output, mcpExplorerProjectionOptions{pointer: "/structuredContent/null", hasSelect: true})
	if got.Value != nil {
		t.Fatalf("null selection value = %#v, want nil", got.Value)
	}
}

func TestMCPExplorerCallProjectionHead(t *testing.T) {
	output := mcpExplorerProjectionTestOutput()
	for _, test := range []struct {
		name      string
		head      int
		wantItems int
		truncated bool
	}{
		{name: "zero", head: 0, wantItems: 0, truncated: true},
		{name: "within bounds", head: 1, wantItems: 1, truncated: true},
		{name: "beyond bounds", head: 3, wantItems: 2, truncated: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := renderMCPExplorerProjection(t, output, mcpExplorerProjectionOptions{pointer: "/structuredContent/items", hasSelect: true, head: test.head, hasHead: true})
			items, ok := got.Value.([]any)
			if !ok || len(items) != test.wantItems || got.Projection.TotalItems == nil || *got.Projection.TotalItems != 2 || got.Projection.ReturnedItems == nil || *got.Projection.ReturnedItems != test.wantItems || got.Projection.Truncated != test.truncated {
				t.Fatalf("head projection = %+v", got)
			}
		})
	}

	var writer bytes.Buffer
	err := writeMCPExplorerCallOutput(&writer, output, mcpExplorerProjectionOptions{pointer: "/structuredContent/name", hasSelect: true, head: 1, hasHead: true})
	if err == nil || err.Error() != "--head requires --select to resolve to an array" || writer.Len() != 0 {
		t.Fatalf("non-array head error/output = %v/%q", err, writer.String())
	}
}

func TestMCPExplorerCallProjectionByteLimitAndErrorBypass(t *testing.T) {
	output := mcpExplorerProjectionTestOutput()
	options := mcpExplorerProjectionOptions{pointer: "/structuredContent/items", hasSelect: true}
	var expected bytes.Buffer
	if err := writeMCPExplorerCallOutput(&expected, output, options); err != nil {
		t.Fatal(err)
	}

	var exact bytes.Buffer
	if err := writeMCPExplorerCallOutput(&exact, output, mcpExplorerProjectionOptions{pointer: options.pointer, hasSelect: true, maxBytes: expected.Len(), hasMaxBytes: true}); err != nil {
		t.Fatalf("exact byte boundary: %v", err)
	}
	if exact.String() != expected.String() || !strings.HasSuffix(exact.String(), "\n") {
		t.Fatalf("exact byte output = %q, want %q", exact.String(), expected.String())
	}

	var oversized bytes.Buffer
	err := writeMCPExplorerCallOutput(&oversized, output, mcpExplorerProjectionOptions{pointer: options.pointer, hasSelect: true, maxBytes: expected.Len() - 1, hasMaxBytes: true})
	if err == nil || err.Error() != "MCP call output is "+strconv.Itoa(expected.Len())+" bytes, exceeding --max-bytes "+strconv.Itoa(expected.Len()-1) || oversized.Len() != 0 {
		t.Fatalf("oversize error/output = %v/%q", err, oversized.String())
	}

	errorOutput := output
	errorOutput.IsError = true
	var bypass bytes.Buffer
	if err := writeMCPExplorerCallOutput(&bypass, errorOutput, mcpExplorerProjectionOptions{pointer: "/missing", hasSelect: true, head: 0, hasHead: true, maxBytes: 0, hasMaxBytes: true}); err != nil {
		t.Fatalf("isError bypass: %v", err)
	}
	var current bytes.Buffer
	if err := json.NewEncoder(&current).Encode(errorOutput); err != nil {
		t.Fatal(err)
	}
	if bypass.String() != current.String() {
		t.Fatalf("isError bypass = %q, want current output %q", bypass.String(), current.String())
	}
}

func TestMCPExplorerCallProjectionPreservesDefaultOutput(t *testing.T) {
	output := mcpExplorerProjectionTestOutput()
	var got, want bytes.Buffer
	if err := writeMCPExplorerCallOutput(&got, output, mcpExplorerProjectionOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(&want).Encode(output); err != nil {
		t.Fatal(err)
	}
	if got.String() != want.String() {
		t.Fatalf("default call output changed:\n got %q\nwant %q", got.String(), want.String())
	}
}

func TestParseMCPExplorerProjectionOptions(t *testing.T) {
	config, err := parseMCPExplorer([]string{"call", "https://mcp.example.test/mcp", "tool", "--select=/structuredContent/items", "--head", "0", "--max-bytes=10"})
	if err != nil {
		t.Fatal(err)
	}
	if !config.projection.hasSelect || config.projection.pointer != "/structuredContent/items" || !config.projection.hasHead || config.projection.head != 0 || !config.projection.hasMaxBytes || config.projection.maxBytes != 10 {
		t.Fatalf("projection options = %#v", config.projection)
	}

	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"call", "https://mcp.example.test/mcp", "tool", "--head", "1"}, want: "--head requires --select"},
		{args: []string{"call", "https://mcp.example.test/mcp", "tool", "--head=-1"}, want: `invalid --head "-1": must be a non-negative integer`},
		{args: []string{"list", "https://mcp.example.test/mcp", "--select", "/tools"}, want: "--select, --head, and --max-bytes are only valid for mcp call"},
		{args: []string{"call", "--command", "/tmp/mcp", "--select", "/content", "--head"}, want: "--head requires a value"},
	}
	for _, test := range tests {
		_, err := parseMCPExplorer(test.args)
		if err == nil || err.Error() != test.want {
			t.Fatalf("parseMCPExplorer(%q) error = %v, want %q", test.args, err, test.want)
		}
	}
}

func mcpExplorerProjectionTestOutput() mcpExplorerCallOutput {
	return mcpExplorerCallOutput{
		ResultType: "complete",
		Content:    nil,
		StructuredContent: map[string]any{
			"items": []any{map[string]any{"name": "first"}, map[string]any{"name": "second"}},
			"a/b":   map[string]any{"~key": "escaped"},
			"name":  "not-an-array",
			"null":  nil,
		},
	}
}

func renderMCPExplorerProjection(t *testing.T, output mcpExplorerCallOutput, options mcpExplorerProjectionOptions) mcpExplorerProjectionOutput {
	t.Helper()
	var rendered bytes.Buffer
	if err := writeMCPExplorerCallOutput(&rendered, output, options); err != nil {
		t.Fatal(err)
	}
	var got mcpExplorerProjectionOutput
	if err := json.Unmarshal(rendered.Bytes(), &got); err != nil {
		t.Fatalf("decode projection: %v", err)
	}
	return got
}
