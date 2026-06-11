package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPInitialize(t *testing.T) {
	t.Parallel()
	resp := handleMCPTestLine(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	result := resp["result"].(map[string]any)
	if result["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("protocolVersion = %v", result["protocolVersion"])
	}
	caps := result["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Fatalf("missing tools capability: %#v", caps)
	}
	serverInfo := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != "jinn" || serverInfo["title"] != "Jinn" {
		t.Fatalf("serverInfo = %#v", serverInfo)
	}
}

func TestMCPToolsListOnlyRouteTool(t *testing.T) {
	t.Parallel()
	resp := handleMCPTestLine(t, `{"jsonrpc":"2.0","id":"tools","method":"tools/list"}`)
	result := resp["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tool count = %d, want 1", len(tools))
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "jinn_route" {
		t.Fatalf("tool name = %v", tool["name"])
	}
	data, _ := json.Marshal(resp)
	if len(data) > 1500 {
		t.Fatalf("tools/list response too large: %d bytes", len(data))
	}
}

func TestMCPToolsCallRouteDoesNotExecute(t *testing.T) {
	t.Parallel()
	resp := handleMCPTestLine(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"jinn_route","arguments":{"need":"run tests"}}}`)
	result := resp["result"].(map[string]any)
	if result["isError"] != false {
		t.Fatalf("isError = %v", result["isError"])
	}
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	var route struct {
		Matches []struct {
			Name string `json:"name"`
			Risk string `json:"risk"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(text), &route); err != nil {
		t.Fatalf("route text is not JSON: %v\n%s", err, text)
	}
	if len(route.Matches) == 0 || route.Matches[0].Name != "run_shell" || route.Matches[0].Risk != "shell" {
		t.Fatalf("unexpected route: %#v", route.Matches)
	}
	if len(text) > 4000 {
		t.Fatalf("default jinn_route output too large: %d bytes", len(text))
	}
}

func TestMCPUnknownMethodAndTool(t *testing.T) {
	t.Parallel()
	methodResp := handleMCPTestLine(t, `{"jsonrpc":"2.0","id":3,"method":"resources/list"}`)
	if code := methodResp["error"].(map[string]any)["code"]; code != float64(-32601) {
		t.Fatalf("unknown method code = %v", code)
	}

	toolResp := handleMCPTestLine(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"main.go"}}}`)
	if code := toolResp["error"].(map[string]any)["code"]; code != float64(-32602) {
		t.Fatalf("unknown tool code = %v", code)
	}
}

func TestMCPNotificationsProduceNoResponse(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	err := runMCP(t.Context(), strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"), &out, "test")
	if err != nil {
		t.Fatalf("runMCP: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("notification produced response: %q", out.String())
	}
}

func TestMCPRunLoopMultipleMessages(t *testing.T) {
	t.Parallel()
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		"",
	}, "\n")
	var out bytes.Buffer
	if err := runMCP(t.Context(), strings.NewReader(input), &out, "test"); err != nil {
		t.Fatalf("runMCP: %v", err)
	}
	scanner := bufio.NewScanner(&out)
	count := 0
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan output: %v", err)
	}
	if count != 2 {
		t.Fatalf("response count = %d, want 2; output=%q", count, out.String())
	}
}

func handleMCPTestLine(t *testing.T, line string) map[string]any {
	t.Helper()
	resp, ok := handleMCPLine([]byte(line), "test")
	if !ok {
		t.Fatal("expected response")
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return decoded
}
