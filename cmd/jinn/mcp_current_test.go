package main

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/voocel/mcp-sdk-go/protocol"
)

const currentMCPMeta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`

func TestMCPCurrentDiscoverAdvertises2026(t *testing.T) {
	t.Parallel()
	resp := handleCurrentMCPTestLine(t, `{"jsonrpc":"2.0","id":"discover","method":"server/discover","params":{`+currentMCPMeta+`}}`)
	result := resp["result"].(map[string]any)
	versions := result["supportedVersions"].([]any)
	if len(versions) != 1 || versions[0] != protocol.Version {
		t.Fatalf("supportedVersions = %#v", versions)
	}
	capabilities := result["capabilities"].(map[string]any)
	if _, ok := capabilities["tools"]; !ok {
		t.Fatalf("tools capability missing: %#v", capabilities)
	}
}

func TestMCPCurrentToolsListKeepsOneToolAndUsesJSONSchema202012(t *testing.T) {
	t.Parallel()
	resp := handleCurrentMCPTestLine(t, `{"jsonrpc":"2.0","id":"tools","method":"tools/list","params":{`+currentMCPMeta+`}}`)
	result := resp["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tool count = %d, want 1", len(tools))
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != mcpRouteTool {
		t.Fatalf("tool name = %v", tool["name"])
	}
	inputSchema := tool["inputSchema"].(map[string]any)
	if inputSchema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("input schema dialect = %v", inputSchema["$schema"])
	}
	outputSchema := tool["outputSchema"].(map[string]any)
	if outputSchema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("output schema dialect = %v", outputSchema["$schema"])
	}
	defs := outputSchema["$defs"].(map[string]any)
	if _, ok := defs["route_match"]; !ok {
		t.Fatalf("output schema missing route_match definition: %#v", defs)
	}
	properties := outputSchema["properties"].(map[string]any)
	matches := properties["matches"].(map[string]any)
	items := matches["items"].(map[string]any)
	if items["$ref"] != "#/$defs/route_match" {
		t.Fatalf("matches items ref = %v", items["$ref"])
	}
}

func TestMCPCurrentRouteReturnsStructuredContent(t *testing.T) {
	t.Parallel()
	resp := handleCurrentMCPTestLine(t, `{"jsonrpc":"2.0","id":"call","method":"tools/call","params":{`+currentMCPMeta+`,"name":"jinn_route","arguments":{"need":"run tests"}}}`)
	result := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("route returned tool error: %#v", result)
	}
	structured := result["structuredContent"].(map[string]any)
	if structured["query"] != "run tests" {
		t.Fatalf("query = %v", structured["query"])
	}
	matches := structured["matches"].([]any)
	if len(matches) == 0 {
		t.Fatalf("expected route matches: %#v", structured)
	}
	content := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content blocks = %d", len(content))
	}
	text := content[0].(map[string]any)["text"].(string)
	var mirrored map[string]any
	if err := json.Unmarshal([]byte(text), &mirrored); err != nil {
		t.Fatalf("content mirror is not JSON: %v", err)
	}
	if mirrored["query"] != structured["query"] {
		t.Fatalf("content mirror query = %v, structured query = %v", mirrored["query"], structured["query"])
	}
}

func TestMCPCurrentRejectsLegacyInitializeAtSDKLayer(t *testing.T) {
	t.Parallel()
	resp := handleCurrentMCPTestLine(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	errResp := resp["error"].(map[string]any)
	if errResp["code"] != float64(protocol.CodeInvalidParams) {
		t.Fatalf("initialize error code = %v", errResp["code"])
	}
}

func TestMCPCurrentRejectsUnsupportedVersion(t *testing.T) {
	t.Parallel()
	resp := handleCurrentMCPTestLine(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-06-18","io.modelcontextprotocol/clientCapabilities":{}}}}`)
	errResp := resp["error"].(map[string]any)
	if errResp["code"] != float64(protocol.CodeUnsupportedProtocolVersion) {
		t.Fatalf("unsupported version code = %v", errResp["code"])
	}
}

func TestMCPCurrentRejectsMissingMeta(t *testing.T) {
	t.Parallel()
	resp := handleCurrentMCPTestLine(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	errResp := resp["error"].(map[string]any)
	if errResp["code"] != float64(protocol.CodeInvalidParams) {
		t.Fatalf("missing metadata code = %v", errResp["code"])
	}
}

func handleCurrentMCPTestLine(t *testing.T, line string) map[string]any {
	t.Helper()
	var msg protocol.Message
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	var responses []*protocol.Message
	newMCPServer("test", jinn.ShellModeDisabled).Handle(context.Background(), &msg, func(response *protocol.Message) error {
		responses = append(responses, response)
		return nil
	})
	if len(responses) != 1 {
		t.Fatalf("response count = %d", len(responses))
	}
	data, err := json.Marshal(responses[0])
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return decoded
}

func TestMCPCurrentRunLoopUsesSDKForCurrentInput(t *testing.T) {
	t.Parallel()
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	defer func() { _ = inputReader.Close() }()
	defer func() { _ = outputReader.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- runMCP(ctx, inputReader, outputWriter, "test", jinn.ShellModeDisabled)
	}()
	line := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + currentMCPMeta + `}}` + "\n"
	if _, err := io.WriteString(inputWriter, line); err != nil {
		t.Fatalf("write MCP request: %v", err)
	}
	var response map[string]any
	decodeErr := make(chan error, 1)
	go func() {
		decodeErr <- json.NewDecoder(outputReader).Decode(&response)
	}()
	select {
	case err := <-decodeErr:
		if err != nil {
			t.Fatalf("decode SDK response: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for SDK response: %v", ctx.Err())
	}
	if response["result"] == nil {
		t.Fatalf("missing SDK result: %#v", response)
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close MCP input: %v", err)
	}
	if err := <-runErr; err != nil {
		t.Fatalf("runMCP: %v", err)
	}
}
