package main

import (
	"reflect"
	"testing"

	"github.com/tiktoken-go/tokenizer"
	"github.com/voocel/mcp-sdk-go/protocol"
)

func TestMCPExplorerTokenizerMatchesOpenAIReference(t *testing.T) {
	// Generated independently with Python tiktoken 0.12.0 and
	// disallowed_special=() so special-token-looking text remains ordinary text.
	tests := []struct {
		encoding tokenizer.Encoding
		text     string
		want     []uint
	}{
		{tokenizer.O200kBase, "hello world", []uint{24912, 2375}},
		{tokenizer.O200kBase, "café Καλημέρα", []uint{66, 103112, 75507, 19058, 17752, 7648}},
		{tokenizer.O200kBase, "👩🏽‍💻🚀", []uint{28823, 102, 52622, 121, 2524, 31446, 119, 112927, 222}},
		{tokenizer.O200kBase, "{\"query\":\"café\",\"limit\":10}\n", []uint{10848, 2975, 7534, 66, 103112, 4294, 19698, 1243, 702, 739}},
		{tokenizer.O200kBase, "func main() { fmt.Println(\"hi\") }\n", []uint{5652, 2758, 416, 354, 18237, 28250, 568, 3686, 1405, 606}},
		{tokenizer.O200kBase, `{"x":"line\n\t\u0000"}`, []uint{10848, 87, 7534, 1137, 3392, 10229, 7570, 1302, 15, 18583}},
		{tokenizer.O200kBase, "<|endoftext|>", []uint{27, 91, 419, 1440, 919, 91, 29}},
		{tokenizer.Cl100kBase, "hello world", []uint{15339, 1917}},
		{tokenizer.Cl100kBase, "café Καλημέρα", []uint{936, 59958, 8008, 248, 19481, 34586, 42524, 44223, 80531, 39179, 19481}},
		{tokenizer.Cl100kBase, "👩🏽‍💻🚀", []uint{9468, 239, 102, 9468, 237, 121, 378, 235, 93273, 119, 9468, 248, 222}},
		{tokenizer.Cl100kBase, "{\"query\":\"café\",\"limit\":10}\n", []uint{5018, 1663, 3332, 936, 59958, 2247, 9696, 794, 605, 534}},
		{tokenizer.Cl100kBase, "func main() { fmt.Println(\"hi\") }\n", []uint{2900, 1925, 368, 314, 9055, 12701, 446, 6151, 909, 457}},
		{tokenizer.Cl100kBase, `{"x":"line\n\t\u0000"}`, []uint{5018, 87, 3332, 1074, 1734, 5061, 3855, 931, 15, 9388}},
		{tokenizer.Cl100kBase, "<|endoftext|>", []uint{27, 91, 8862, 728, 428, 91, 29}},
	}
	for _, test := range tests {
		codec, err := tokenizer.Get(test.encoding)
		if err != nil {
			t.Fatalf("Get(%s): %v", test.encoding, err)
		}
		got, _, err := codec.Encode(test.text)
		if err != nil {
			t.Fatalf("Encode(%s, %q): %v", test.encoding, test.text, err)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("Encode(%s, %q) = %v, want %v", test.encoding, test.text, got, test.want)
		}
	}
}

func TestMCPExplorerCostOutputUsesExactRenderers(t *testing.T) {
	server, discovery, tools := mcpExplorerCostFixture()
	output, err := newMCPExplorerCostOutput(discovery, tools, "")
	if err != nil {
		t.Fatal(err)
	}
	assertMCPExplorerCostOutput(t, output, server, discovery, tools)
}

func mcpExplorerCostFixture() (*protocol.Implementation, *protocol.DiscoverResult, []*protocol.Tool) {
	server := &protocol.Implementation{Name: "example", Version: "1.0"}
	discovery := &protocol.DiscoverResult{
		WithMeta:          protocol.WithMeta{Meta: protocol.ResultMeta{ServerInfo: server}},
		SupportedVersions: []string{protocol.Version},
		Capabilities:      protocol.ServerCapabilities{Tools: &protocol.ToolsCapability{}},
	}
	tools := []*protocol.Tool{
		{Name: "search", Description: "Search the catalog.", InputSchema: protocol.JSONSchema{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "description": "Text to find."}, "limit": map[string]any{"type": "integer", "default": 10}}, "required": []any{"query"}, "additionalProperties": false}},
		{Name: "status", Description: "Return status.", InputSchema: protocol.JSONSchema{"type": "object", "properties": map[string]any{}, "additionalProperties": false}},
	}
	return server, discovery, tools
}

func assertMCPExplorerCostOutput(t *testing.T, output mcpExplorerCostOutput, server *protocol.Implementation, discovery *protocol.DiscoverResult, tools []*protocol.Tool) {
	t.Helper()
	if output.SchemaVersion != 1 || output.Encoding != "o200k_base" || output.ToolCount != len(tools) {
		t.Fatalf("cost identity = %+v", output)
	}
	canonical, err := renderMCPExplorerJSON(mcpExplorerListOutput{Server: server, Discovery: discovery, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	signatures, err := renderMCPExplorerJSON(newMCPExplorerSignaturesOutput(server, tools))
	if err != nil {
		t.Fatal(err)
	}
	codec, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		t.Fatal(err)
	}
	wantCanonical, err := mcpExplorerFormatCost(codec, canonical)
	if err != nil {
		t.Fatal(err)
	}
	wantSignatures, err := mcpExplorerFormatCost(codec, signatures)
	if err != nil {
		t.Fatal(err)
	}
	if output.Formats.CanonicalList != wantCanonical || output.Formats.SignatureList != wantSignatures {
		t.Fatalf("format costs = %+v, want canonical=%+v signatures=%+v", output.Formats, wantCanonical, wantSignatures)
	}
	if canonical[len(canonical)-1] != '\n' || signatures[len(signatures)-1] != '\n' {
		t.Fatal("renderer omitted the counted trailing newline")
	}
	if output.Savings.Tokens != wantCanonical.Tokens-wantSignatures.Tokens || len(output.LargestTools) != len(tools) {
		t.Fatalf("savings/largest tools = %+v", output)
	}
}

func TestMCPExplorerTokenizerRejectsModelNames(t *testing.T) {
	if _, _, err := mcpExplorerTokenizer("gpt-4o"); err == nil || err.Error() != `invalid --encoding "gpt-4o": use o200k_base or cl100k_base` {
		t.Fatalf("model-name encoding error = %v", err)
	}
}
