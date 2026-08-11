package main

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/voocel/mcp-sdk-go/protocol"
)

func TestMCPExplorerHumanRendererEscapesHostileControls(t *testing.T) {
	value := map[string]any{
		"key\u202e": []any{"ansi\x1b[31m", "csi\u009b31m", "zero\u180e\u200b\u2061", "bom\ufeff", "bell\a", "music\U0001d173"},
		`key\u202e`: "literal escape remains distinct",
	}
	rendered, err := renderMCPExplorerHuman(value)
	if err != nil {
		t.Fatal(err)
	}
	text := string(rendered)
	renderedAgain, err := renderMCPExplorerHuman(value)
	if err != nil || string(renderedAgain) != text {
		t.Fatalf("human output is not deterministic: %q, %v", renderedAgain, err)
	}
	for _, raw := range []string{"\x1b", "\u009b", "\u180e", "\u202e", "\u200b", "\u2061", "\ufeff", "\a", "\U0001d173"} {
		if strings.Contains(text, raw) {
			t.Fatalf("human output retained raw hostile control %q: %q", raw, text)
		}
	}
	for _, escaped := range []string{`\\u001b`, `\\u009b`, `\\u180e`, `\\u202e`, `\\u200b`, `\\u2061`, `\\ufeff`, `\\u0007`, `\\U0001d173`} {
		if !strings.Contains(text, escaped) {
			t.Fatalf("human output omitted escaped control %q: %q", escaped, text)
		}
	}
	var output bytes.Buffer
	if err := writeMCPExplorerOutput(&output, "", map[string]string{"status": "ok"}); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "{\"status\":\"ok\"}\n"; got != want {
		t.Fatalf("default output = %q, want %q", got, want)
	}
}

func TestMCPExplorerExportSortsAndPreservesSchemas(t *testing.T) {
	schema := protocol.JSONSchema{
		"type":                 "object",
		"properties":           map[string]any{"query": map[string]any{"type": "string", "minLength": 2}},
		"required":             []any{"query"},
		"additionalProperties": false,
	}
	tools := []*protocol.Tool{
		{Name: "zeta", Description: "Z", InputSchema: protocol.JSONSchema{"type": "object"}},
		{Name: "alpha", Description: "A", InputSchema: schema},
	}
	value, err := newMCPExportOutput("openai-responses", tools)
	if err != nil {
		t.Fatal(err)
	}
	functions, ok := value.([]mcpExportResponsesFunction)
	if !ok || len(functions) != 2 || functions[0].Name != "alpha" || functions[1].Name != "zeta" {
		t.Fatalf("export functions = %#v", value)
	}
	if !reflect.DeepEqual(functions[0].Parameters, schema) || functions[0].Type != "function" || functions[0].Strict {
		t.Fatalf("export function = %#v", functions[0])
	}
}

func TestMCPExplorerExportRejectsResponseNamesButPreservesMCPNames(t *testing.T) {
	tools := []*protocol.Tool{{Name: "not allowed", InputSchema: protocol.JSONSchema{"type": "object"}}}
	if _, err := newMCPExportOutput("openai-responses", tools); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("openai-responses invalid name error = %v", err)
	}
	value, err := newMCPExportOutput("mcp", tools)
	if err != nil {
		t.Fatalf("mcp export rejected native name: %v", err)
	}
	output := value.(mcpExportMCPOutput)
	if output.Tools[0].Name != "not allowed" {
		t.Fatalf("mcp export renamed tool: %#v", output)
	}
	if _, err := newMCPExportOutput("mcp", []*protocol.Tool{nil}); err == nil {
		t.Fatal("mcp export accepted null tool")
	}
}

func TestMCPExplorerFormatParserCompatibility(t *testing.T) {
	config, err := parseMCPExplorer([]string{"list", "https://mcp.example.test/mcp"})
	if err != nil || config.format != "" {
		t.Fatalf("default list config = %#v, %v", config, err)
	}
	config, err = parseMCPExplorer([]string{"inspect", "https://mcp.example.test/mcp", "tool", "--format=human"})
	if err != nil || config.format != mcpExplorerFormatHuman {
		t.Fatalf("human inspect config = %#v, %v", config, err)
	}
	config, err = parseMCPExplorer([]string{"export", "https://mcp.example.test/mcp", "--format=openai-responses"})
	if err != nil || config.format != "openai-responses" {
		t.Fatalf("export config = %#v, %v", config, err)
	}
	if _, err := parseMCPExplorer([]string{"export", "https://mcp.example.test/mcp", "--format=human"}); err == nil {
		t.Fatal("export accepted human output")
	}
}

func TestMCPExplorerHumanToolErrorBypassesProjectionAndByteLimit(t *testing.T) {
	var output bytes.Buffer
	projection := mcpExplorerProjectionOptions{pointer: "/missing", hasSelect: true, maxBytes: 0, hasMaxBytes: true}
	err := writeMCPExplorerCallFormattedOutput(&output, mcpExplorerFormatHuman, mcpExplorerCallOutput{ResultType: "complete", IsError: true}, projection)
	if err != nil {
		t.Fatalf("human tool error was projected or limited: %v", err)
	}
	if !strings.Contains(output.String(), `"isError": true`) {
		t.Fatalf("human tool error output = %q", output.String())
	}
}

func TestMCPExplorerHumanMaxBytesMeasuresWrittenOutput(t *testing.T) {
	call := mcpExplorerCallOutput{ResultType: "complete", StructuredContent: map[string]any{"items": []any{"one", "two"}}}
	projection := mcpExplorerProjectionOptions{pointer: "/structuredContent/items", hasSelect: true}
	value, err := projectMCPExplorerCallValue(call, projection)
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := renderMCPExplorerHuman(value)
	if err != nil {
		t.Fatal(err)
	}
	projection.maxBytes, projection.hasMaxBytes = len(rendered)-1, true
	var output bytes.Buffer
	err = writeMCPExplorerCallFormattedOutput(&output, mcpExplorerFormatHuman, call, projection)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("is %d bytes", len(rendered))) || output.Len() != 0 {
		t.Fatalf("human max-bytes error = %v, output=%q", err, output.String())
	}
	projection.maxBytes = len(rendered)
	if err := writeMCPExplorerCallFormattedOutput(&output, mcpExplorerFormatHuman, call, projection); err != nil || output.Len() != len(rendered) {
		t.Fatalf("exact human max-bytes = %v, bytes=%d want=%d", err, output.Len(), len(rendered))
	}
}

func TestMCPServersNamesFormatAndOrdering(t *testing.T) {
	format, err := parseMCPServersListFormat([]string{"--format=names"})
	if err != nil || format != "names" {
		t.Fatalf("names format = %q, %v", format, err)
	}
	if _, err := parseMCPServersListFormat([]string{"--format=xml"}); err == nil {
		t.Fatal("invalid names format accepted")
	}
	names := mcpServerNames(mcpServerListOutput{Servers: []mcpServerListItem{{Name: "alpha"}, {Name: "zeta"}}})
	if got, want := strings.Join(names, "\n"), "alpha\nzeta"; got != want {
		t.Fatalf("names = %q, want %q", got, want)
	}
}
