package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/voocel/mcp-sdk-go/client"
	"github.com/voocel/mcp-sdk-go/protocol"
	"github.com/voocel/mcp-sdk-go/transport/mem"
)

func questionnaireArguments() map[string]any {
	return map[string]any{"questions": []any{map[string]any{
		"question": "Implement this plan?", "header": "Approval",
		"options": []any{map[string]any{"value": "approve", "label": "Approve plan", "description": "Begin.", "recommended": true}, map[string]any{"value": "stop", "label": "Do not implement", "description": "Stop."}},
	}}}
}

func TestMCPAskUserQuestionSchemaIncludesValueAndRecommendation(t *testing.T) {
	t.Parallel()
	questions := mcpAskUserInputSchema["properties"].(map[string]any)["questions"].(map[string]any)
	question := questions["items"].(map[string]any)
	options := question["properties"].(map[string]any)["options"].(map[string]any)
	option := options["items"].(map[string]any)
	properties := option["properties"].(map[string]any)
	if properties["value"] == nil || properties["recommended"] == nil {
		t.Fatalf("option properties = %#v", properties)
	}
	required := option["required"].([]string)
	if !slices.Contains(required, "value") {
		t.Fatalf("required = %#v", required)
	}
}

func TestMCPAskUserQuestionRequiresElicitationCapability(t *testing.T) {
	t.Parallel()
	response := handleMCPServerTestLine(t, newMCPServer("test", jinn.ShellModeDisabled), mcpToolCallRequest(t, "ask", mcpAskUserTool, questionnaireArguments()))
	assertMCPToolErrorContains(t, response, "form elicitation support")
}

func TestMCPAskUserQuestionCompletesMRTR(t *testing.T) {
	t.Parallel()
	srv := newMCPServer("test", jinn.ShellModeDisabled)
	mcpClient := client.New(mem.New(srv), &client.Options{
		Info: &protocol.Implementation{Name: "questionnaire-test", Version: "1"},
		Elicitor: func(_ context.Context, params *protocol.ElicitParams) (*protocol.ElicitResult, error) {
			if !strings.Contains(params.Message, "structured questions") {
				t.Fatalf("message = %q", params.Message)
			}
			return &protocol.ElicitResult{Action: protocol.ElicitActionAccept, Content: map[string]any{"question_1_choice": "Approve plan"}}, nil
		},
	})
	t.Cleanup(func() {
		if err := mcpClient.Close(); err != nil {
			t.Errorf("close MCP client: %v", err)
		}
	})
	response, err := mcpClient.CallTool(t.Context(), &protocol.CallToolParams{Name: mcpAskUserTool, Arguments: questionnaireArguments()})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(response.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"answer":"Approve plan"`) || !strings.Contains(string(data), `"value":"approve"`) || !strings.Contains(string(data), `"recommended":true`) || !strings.Contains(string(data), `"cancelled":false`) {
		t.Fatalf("response = %s", data)
	}
}
