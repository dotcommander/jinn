package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/jinn/internal/jinn"
)

func TestMCPInitialize(t *testing.T) {
	t.Parallel()
	resp := handleMCPTestLine(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	result := resp["result"].(map[string]any)
	if result["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("protocolVersion = %v", result["protocolVersion"])
	}
	instructions, _ := result["instructions"].(string)
	for _, required := range []string{"Before naming", "development capability or tool", "call jinn_route", "never infer Jinn tool names"} {
		if !strings.Contains(instructions, required) {
			t.Fatalf("instructions = %q, want %q", instructions, required)
		}
	}
	if len(instructions) > 512 {
		t.Fatalf("instructions length = %d, want <= 512", len(instructions))
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

func TestMCPToolsListIncludesCompactQuestionnaireSurface(t *testing.T) {
	t.Parallel()
	resp := handleMCPTestLine(t, `{"jsonrpc":"2.0","id":"tools","method":"tools/list"}`)
	result := resp["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("tool count = %d, want 2", len(tools))
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "jinn_route" {
		t.Fatalf("tool name = %v", tool["name"])
	}
	if title, _ := tool["title"].(string); !strings.Contains(strings.ToLower(title), "call first") {
		t.Fatalf("tool title does not make discovery order explicit: %q", title)
	}
	if description, _ := tool["description"].(string); !strings.Contains(description, "development task") || !strings.Contains(description, "side-effect-free") {
		t.Fatalf("tool description does not make discovery behavior explicit: %q", description)
	}
	questionnaire := tools[1].(map[string]any)
	if questionnaire["name"] != mcpAskUserTool {
		t.Fatalf("questionnaire tool name = %v", questionnaire["name"])
	}
	inputSchema := questionnaire["inputSchema"].(map[string]any)
	required := inputSchema["required"].([]any)
	if len(required) != 1 || required[0] != "questions" {
		t.Fatalf("questionnaire required fields = %#v", required)
	}
	data, _ := json.Marshal(resp)
	if len(data) > 3000 {
		t.Fatalf("tools/list response too large: %d bytes", len(data))
	}
}

func TestLegacyMCPRequiresFormElicitation(t *testing.T) {
	t.Parallel()
	if legacyMCPClientSupportsElicitation(json.RawMessage(`{"capabilities":{"elicitation":{"url":{}}}}`)) {
		t.Fatal("URL-only elicitation capability was accepted as form support")
	}
	if !legacyMCPClientSupportsElicitation(json.RawMessage(`{"capabilities":{"elicitation":{"form":{}}}}`)) {
		t.Fatal("form elicitation capability was rejected")
	}
}

func TestMCPLegacyAskUserElicitationRoundTrip(t *testing.T) {
	t.Parallel()
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	t.Cleanup(func() {
		_ = inputReader.Close()
		_ = inputWriter.Close()
		_ = outputReader.Close()
		_ = outputWriter.Close()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	runErr := make(chan error, 1)
	go func() {
		runErr <- runLegacyMCP(ctx, inputReader, outputWriter, "test", jinn.ShellModeDisabled)
	}()
	decoder := json.NewDecoder(outputReader)

	writeLegacyMCPTestLine(t, inputWriter, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"elicitation":{"form":{}}}}}`)
	var initialize map[string]any
	if err := decoder.Decode(&initialize); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	writeLegacyMCPTestLine(t, inputWriter, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	writeLegacyMCPTestLine(t, inputWriter, `{"jsonrpc":"2.0","id":"call","method":"tools/call","params":{"name":"ask_user_question","arguments":{"questions":[{"question":"Did the form render?","header":"MCP smoke","options":[{"value":"pass","label":"Pass","description":"Rendered","recommended":true},{"value":"fail","label":"Fail","description":"Did not render"}]}]}}}`)

	var elicitation map[string]any
	if err := decoder.Decode(&elicitation); err != nil {
		t.Fatalf("decode elicitation request: %v", err)
	}
	if elicitation["method"] != "elicitation/create" {
		t.Fatalf("elicitation method = %v, response = %#v", elicitation["method"], elicitation)
	}
	params := elicitation["params"].(map[string]any)
	if params["mode"] != "form" {
		t.Fatalf("elicitation mode = %v", params["mode"])
	}
	schema := params["requestedSchema"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	choice := properties["question_1_choice"].(map[string]any)
	if choice["default"] != "Pass" {
		t.Fatalf("recommended default = %v", choice["default"])
	}
	id, err := json.Marshal(elicitation["id"])
	if err != nil {
		t.Fatalf("marshal elicitation id: %v", err)
	}
	writeLegacyMCPTestLine(t, inputWriter, `{"jsonrpc":"2.0","id":`+string(id)+`,"result":{"action":"accept","content":{"question_1_choice":"Pass"}}}`)

	var call map[string]any
	if err := decoder.Decode(&call); err != nil {
		t.Fatalf("decode tool response: %v", err)
	}
	result := call["result"].(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	answers := structured["answers"].([]any)
	choices := answers[0].(map[string]any)["choices"].([]any)
	if value := choices[0].(map[string]any)["value"]; value != "pass" {
		t.Fatalf("selected stable value = %v", value)
	}

	if err := inputWriter.Close(); err != nil {
		t.Fatalf("close MCP input: %v", err)
	}
	if err := <-runErr; err != nil {
		t.Fatalf("runLegacyMCP: %v", err)
	}
}

func writeLegacyMCPTestLine(t *testing.T, writer io.Writer, line string) {
	t.Helper()
	if _, err := io.WriteString(writer, line+"\n"); err != nil {
		t.Fatalf("write MCP line: %v", err)
	}
}

func TestMCPInstructionsRequireRouteBeforeSelection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		profile      mcpProfile
		wantExecutor bool
	}{
		{profile: mcpProfileDiscover},
		{profile: mcpProfileReadOnly, wantExecutor: true},
		{profile: mcpProfileNetwork, wantExecutor: true},
	}
	for _, tt := range tests {
		t.Run(string(tt.profile), func(t *testing.T) {
			t.Parallel()
			instructions := mcpInstructions(tt.profile)
			for _, required := range []string{
				"Before naming",
				"call jinn_route",
				"even when a tool name seems obvious",
				"never infer Jinn tool names from memory",
			} {
				if !strings.Contains(instructions, required) {
					t.Fatalf("instructions %q do not contain %q", instructions, required)
				}
			}
			if len(instructions) > 512 {
				t.Fatalf("instructions are %d bytes, want at most 512", len(instructions))
			}
			if got := strings.Contains(instructions, "then use jinn_call"); got != tt.wantExecutor {
				t.Fatalf("instructions executor guidance = %t, want %t: %q", got, tt.wantExecutor, instructions)
			}
		})
	}
}

func TestMCPCallDescriptionsRequireRouteFirst(t *testing.T) {
	t.Parallel()
	for _, profile := range []mcpProfile{mcpProfileReadOnly, mcpProfileNetwork} {
		t.Run(string(profile), func(t *testing.T) {
			t.Parallel()
			description := mcpCallDescription(profile)
			if !strings.Contains(description, "Call jinn_route first") || !strings.Contains(description, "even if the tool or arguments seem obvious") {
				t.Fatalf("jinn_call description does not require routing first: %q", description)
			}
		})
	}
}

func TestMCPToolsCallRouteDoesNotExecute(t *testing.T) {
	t.Parallel()
	resp := handleMCPTestLine(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"jinn_route","arguments":{"need":"read source file"}}}`)
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
	if len(route.Matches) != 1 {
		t.Fatalf("default route returned %d matches, want 1: %#v", len(route.Matches), route.Matches)
	}
	if route.Matches[0].Name != "read_file" || route.Matches[0].Risk != "read_only" {
		t.Fatalf("unexpected route: %#v", route.Matches)
	}
	if len(text) > 4000 {
		t.Fatalf("default jinn_route output too large: %d bytes", len(text))
	}
}

func TestMCPLegacyRouteIgnoresAgentHostControlArguments(t *testing.T) {
	t.Parallel()
	resp := handleMCPTestLine(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"jinn_route","arguments":{"need":"run tests","intent":"choose a capability","accept_large_output":true}}}`)
	if errResponse, ok := resp["error"]; ok {
		t.Fatalf("route rejected agent host control arguments: %v", errResponse)
	}
}

func TestMCPLegacyRouteStillRejectsUnknownArguments(t *testing.T) {
	t.Parallel()
	resp := handleMCPTestLine(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"jinn_route","arguments":{"need":"run tests","unexpected":true}}}`)
	errResponse, ok := resp["error"].(map[string]any)
	message, _ := errResponse["message"].(string)
	if !ok || !strings.Contains(message, "unknown field") {
		t.Fatalf("unknown argument response = %#v", resp)
	}
}

func TestMCPRouteRunPlanClassification(t *testing.T) {
	t.Parallel()
	resp := handleMCPTestLine(t, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"jinn_route","arguments":{"need":"run plan","include_mutating":true,"max_tools":8}}}`)
	matches := decodeMCPRouteMatches(t, resp)
	for _, match := range matches {
		if match.Name == "run_plan" {
			if !match.Mutating || match.Risk != "mutating" {
				t.Fatalf("run_plan classification = mutating:%v risk:%q", match.Mutating, match.Risk)
			}
			return
		}
	}
	t.Fatalf("run_plan missing from MCP route: %+v", matches)
}

func TestMCPRouteRunPlanExcludedWhenMutatingDisabled(t *testing.T) {
	t.Parallel()
	resp := handleMCPTestLine(t, `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"jinn_route","arguments":{"need":"run plan","include_mutating":false,"max_tools":8}}}`)
	for _, match := range decodeMCPRouteMatches(t, resp) {
		if match.Name == "run_plan" {
			t.Fatalf("run_plan returned with include_mutating=false: %+v", match)
		}
	}
}

type mcpRouteMatch struct {
	Name     string `json:"name"`
	Mutating bool   `json:"mutating"`
	Risk     string `json:"risk"`
}

func decodeMCPRouteMatches(t *testing.T, resp map[string]any) []mcpRouteMatch {
	t.Helper()
	result := resp["result"].(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	var route struct {
		Matches []mcpRouteMatch `json:"matches"`
	}
	if err := json.Unmarshal([]byte(text), &route); err != nil {
		t.Fatalf("route text is not JSON: %v\n%s", err, text)
	}
	return route.Matches
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
	err := runMCP(t.Context(), strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n"), &out, "test", jinn.ShellModeDisabled)
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
	if err := runMCP(t.Context(), strings.NewReader(input), &out, "test", jinn.ShellModeDisabled); err != nil {
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
	resp, ok := handleMCPLine([]byte(line), "test", jinn.ShellModeDisabled)
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
