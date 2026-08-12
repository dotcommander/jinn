package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/voocel/mcp-sdk-go/protocol"
	"github.com/voocel/mcp-sdk-go/server"
)

const currentMCPMeta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`

func TestDetectPipedMCPPreservesInputAndSelectsProtocol(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		mcp   bool
	}{
		{name: "legacy initialize", input: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n", mcp: true},
		{name: "current discovery", input: `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + currentMCPMeta + `}}` + "\n", mcp: true},
		{name: "one-shot tool request", input: `{"tool":"read_file","args":{"path":"README.md"}}`, mcp: false},
		{name: "pretty one-shot request", input: "{\n  \"tool\": \"read_file\",\n  \"args\": {\"path\": \"README.md\"}\n}\n", mcp: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			replay, gotMCP := detectPipedMCP(strings.NewReader(tt.input))
			if gotMCP != tt.mcp {
				t.Fatalf("MCP = %v, want %v", gotMCP, tt.mcp)
			}
			gotInput, err := io.ReadAll(replay)
			if err != nil {
				t.Fatalf("read replay: %v", err)
			}
			if string(gotInput) != tt.input {
				t.Fatalf("replayed input = %q, want %q", gotInput, tt.input)
			}
		})
	}
}

func TestMCPProfileParsing(t *testing.T) {
	t.Parallel()
	mode, profile, positional, err := parseCLIArgs([]string{"--mcp-profile", "read-only", "--mcp"})
	if err != nil {
		t.Fatalf("parseCLIArgs: %v", err)
	}
	if mode != jinn.ShellModeDisabled || profile != mcpProfileReadOnly || len(positional) != 1 || positional[0] != "--mcp" {
		t.Fatalf("parsed args = mode:%q profile:%q positional:%#v", mode, profile, positional)
	}
	if _, err := parseMCPProfile("unsafe"); err == nil {
		t.Fatal("parseMCPProfile accepted an unsupported profile")
	}
	for _, arguments := range [][]string{
		{"--mcp-profile=discover"},
		{"--mcp-profile", "read-only", "--schema"},
		{"--mcp-profile=read-only", "--schema", "--mcp"},
	} {
		if _, _, _, err := parseCLIArgs(arguments); err == nil || !strings.Contains(err.Error(), "--mcp-profile requires --mcp or --mcp-http") {
			t.Fatalf("parseCLIArgs(%q) error = %v, want MCP transport requirement", arguments, err)
		}
	}
}

func TestParseCLIArgsPreservesMCPExplorerCommandArguments(t *testing.T) {
	t.Parallel()
	want := []string{"mcp", "inspect", "--command", "/bin/echo", "--arg", "--mcp-profile", "--arg", "network", "jinn_route"}
	mode, profile, positional, err := parseCLIArgs(append([]string{"--shell-mode=sandboxed"}, want...))
	if err != nil {
		t.Fatalf("parseCLIArgs: %v", err)
	}
	if mode != jinn.ShellModeSandboxed || profile != mcpProfileDiscover || !slices.Equal(positional, want) {
		t.Fatalf("parsed args = mode:%q profile:%q positional:%#v", mode, profile, positional)
	}
}

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
	if title, _ := tool["title"].(string); !strings.Contains(strings.ToLower(title), "call first") {
		t.Fatalf("tool title does not make discovery order explicit: %q", title)
	}
	if description, _ := tool["description"].(string); !strings.Contains(description, "development task") || !strings.Contains(description, "side-effect-free") {
		t.Fatalf("tool description does not make discovery behavior explicit: %q", description)
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
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal tools/list response: %v", err)
	}
	if len(data) > 3000 {
		t.Fatalf("current tools/list response too large: %d bytes", len(data))
	}
}

//nolint:gocognit,gocyclo,revive // One end-to-end test intentionally validates the complete profile contract.
func TestMCPReadOnlyProfileAddsCallToolWithReadOnlyAllowlist(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	engine, err := jinn.NewWithConfig(workspace, jinn.EngineConfig{Version: "test", ShellMode: jinn.ShellModeDisabled})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = engine.Close() }()

	resp := handleMCPServerTestLine(t, newMCPServerWithProfile("test", jinn.ShellModeDisabled, mcpProfileReadOnly, engine), `{"jsonrpc":"2.0","id":"tools","method":"tools/list","params":{`+currentMCPMeta+`}}`)
	result := resp["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tool count = %d, want 2: %#v", len(tools), tools)
	}
	seen := make(map[string]bool, len(tools))
	for _, rawTool := range tools {
		tool := rawTool.(map[string]any)
		name := tool["name"].(string)
		seen[name] = true
		if name != mcpRouteTool && name != mcpCallTool {
			t.Fatalf("unexpected profile tool: %q", name)
		}
		if name == mcpRouteTool {
			inputSchema := tool["inputSchema"].(map[string]any)
			properties := inputSchema["properties"].(map[string]any)
			includeMutating := properties["include_mutating"].(map[string]any)
			if includeMutating["default"] != false || strings.Contains(includeMutating["description"].(string), "Allow recommendations") {
				t.Fatalf("read-only route schema advertises mutation: %#v", includeMutating)
			}
		}
		if name == mcpCallTool {
			description, _ := tool["description"].(string)
			if !strings.Contains(description, "Call jinn_route first") {
				t.Fatalf("jinn_call description does not require routing first: %q", description)
			}
			inputSchema := tool["inputSchema"].(map[string]any)
			properties := inputSchema["properties"].(map[string]any)
			enum := properties["tool"].(map[string]any)["enum"].([]any)
			for _, value := range enum {
				if value == "write_file" || value == "run_shell" || value == "memory" {
					t.Fatalf("mutating tool leaked into read-only enum: %v", value)
				}
			}
		}
	}
	if !seen[mcpRouteTool] || !seen[mcpCallTool] {
		t.Fatalf("profile tools = %#v", seen)
	}
}

func TestMCPReadOnlyProfileCallExecutesReadOnlyToolAndRejectsMutation(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("hello from MCP\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	engine, err := jinn.NewWithConfig(workspace, jinn.EngineConfig{Version: "test", ShellMode: jinn.ShellModeDisabled})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = engine.Close() }()
	srv := newMCPServerWithProfile("test", jinn.ShellModeDisabled, mcpProfileReadOnly, engine)

	readResp := handleMCPServerTestLine(t, srv, `{"jsonrpc":"2.0","id":"read","method":"tools/call","params":{`+currentMCPMeta+`,"name":"jinn_call","arguments":{"tool":"read_file","arguments":{"path":"hello.txt"},"compress":false}}}`)
	readResult := readResp["result"].(map[string]any)
	if readResult["isError"] == true {
		t.Fatalf("read-only call returned tool error: %#v", readResult)
	}
	structured := readResult["structuredContent"].(map[string]any)
	if structured["tool"] != "read_file" || !strings.Contains(structured["result"].(string), "hello from MCP") {
		t.Fatalf("unexpected read-only result: %#v", structured)
	}

	writeResp := handleMCPServerTestLine(t, srv, `{"jsonrpc":"2.0","id":"write","method":"tools/call","params":{`+currentMCPMeta+`,"name":"jinn_call","arguments":{"tool":"write_file","arguments":{"path":"blocked.txt","content":"nope"}}}}`)
	writeResult := writeResp["result"].(map[string]any)
	if writeResult["isError"] != true {
		t.Fatalf("mutating call was not rejected: %#v", writeResult)
	}
	text := writeResult["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "unavailable in this MCP profile") {
		t.Fatalf("unexpected mutation rejection: %q", text)
	}
}

func TestMCPReadOnlyProfileForcesRouteAndShellPolicy(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	engine, err := jinn.NewWithConfig(workspace, jinn.EngineConfig{Version: "test", ShellMode: jinn.ShellModeUnsafe})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = engine.Close() }()

	srv := newMCPServerWithProfile("test", jinn.ShellModeUnsafe, mcpProfileReadOnly, engine)
	resp := handleMCPServerTestLine(t, srv, `{"jsonrpc":"2.0","id":"route","method":"tools/call","params":{`+currentMCPMeta+`,"name":"jinn_route","arguments":{"need":"write files and run shell commands","include_mutating":true}}}`)
	result := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("read-only route returned tool error: %#v", result)
	}
	structured := result["structuredContent"].(map[string]any)
	for _, rawMatch := range structured["matches"].([]any) {
		match := rawMatch.(map[string]any)
		if match["mutating"] == true || match["name"] == "run_shell" {
			t.Fatalf("read-only route leaked unsafe match: %#v", match)
		}
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

func TestMCPCurrentRouteIgnoresAgentHostControlArguments(t *testing.T) {
	t.Parallel()
	resp := handleCurrentMCPTestLine(t, `{"jsonrpc":"2.0","id":"call","method":"tools/call","params":{`+currentMCPMeta+`,"name":"jinn_route","arguments":{"need":"run tests","intent":"choose a capability","accept_large_output":true}}}`)
	result := resp["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("route rejected agent host control arguments: %#v", result)
	}
	structured := result["structuredContent"].(map[string]any)
	if structured["query"] != "run tests" {
		t.Fatalf("query = %v", structured["query"])
	}
}

func TestMCPCurrentRouteRejectsSchemaViolations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		args     string
		contains string
	}{
		{name: "unknown outer field", args: `"need":"read files","unexpected":true`, contains: "unknown field"},
		{name: "zero max tools", args: `"need":"read files","max_tools":0`, contains: "max_tools"},
		{name: "max tools above schema maximum", args: `"need":"read files","max_tools":9`, contains: "max_tools"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := handleCurrentMCPTestLine(t, `{"jsonrpc":"2.0","id":"route","method":"tools/call","params":{`+currentMCPMeta+`,"name":"jinn_route","arguments":{`+tt.args+`}}}`)
			assertMCPToolErrorContains(t, resp, tt.contains)
		})
	}
}

func TestMCPReadOnlyCallRejectsUnknownOuterArgument(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	engine, err := jinn.NewWithConfig(workspace, jinn.EngineConfig{Version: "test", ShellMode: jinn.ShellModeDisabled})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = engine.Close() }()

	resp := handleMCPServerTestLine(t, newMCPServerWithProfile("test", jinn.ShellModeDisabled, mcpProfileReadOnly, engine), `{"jsonrpc":"2.0","id":"call","method":"tools/call","params":{`+currentMCPMeta+`,"name":"jinn_call","arguments":{"tool":"read_file","unexpected":true}}}`)
	assertMCPToolErrorContains(t, resp, "unknown field")
}

func TestMCPCurrentRejectsLegacyInitializeAtSDKLayer(t *testing.T) {
	t.Parallel()
	resp := handleCurrentMCPTestLine(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	errResp := resp["error"].(map[string]any)
	if errResp["code"] != float64(protocol.CodeInvalidParams) {
		t.Fatalf("initialize error code = %v", errResp["code"])
	}
}

func TestMCPReadOnlyRunLoopRejectsLegacyInitializeAtSDKLayer(t *testing.T) {
	t.Parallel()
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	defer func() { _ = inputReader.Close() }()
	defer func() { _ = outputReader.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- runMCPWithProfile(ctx, inputReader, outputWriter, "test", jinn.ShellModeUnsafe, mcpProfileReadOnly)
	}()
	if _, err := io.WriteString(inputWriter, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n"); err != nil {
		t.Fatalf("write legacy initialize: %v", err)
	}
	var resp map[string]any
	decodeErr := make(chan error, 1)
	go func() { decodeErr <- json.NewDecoder(outputReader).Decode(&resp) }()
	select {
	case err := <-decodeErr:
		if err != nil {
			t.Fatalf("decode response: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("timed out waiting for legacy rejection: %v", ctx.Err())
	}
	errResp, ok := resp["error"].(map[string]any)
	if !ok || errResp["code"] != float64(protocol.CodeInvalidParams) {
		t.Fatalf("read-only legacy initialize response = %#v", resp)
	}
	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close MCP input: %v", err)
	}
	if err := <-runErr; err != nil {
		t.Fatalf("runMCPWithProfile: %v", err)
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
	return handleMCPServerTestLine(t, newMCPServer("test", jinn.ShellModeDisabled), line)
}

func handleMCPServerTestLine(t *testing.T, srv *server.Server, line string) map[string]any {
	t.Helper()
	var msg protocol.Message
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	var responses []*protocol.Message
	srv.Handle(context.Background(), &msg, func(response *protocol.Message) error {
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

func assertMCPToolErrorContains(t *testing.T, resp map[string]any, want string) {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing tool result: %#v", resp)
	}
	if result["isError"] != true {
		t.Fatalf("expected tool error: %#v", result)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("missing tool error content: %#v", result)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, want) {
		t.Fatalf("error text %q does not contain %q", text, want)
	}
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
