package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/voocel/mcp-sdk-go/protocol"
	"github.com/voocel/mcp-sdk-go/server"
)

func mcpAskUserHandler(_ context.Context, req *server.CallRequest) (protocol.ToolResponse, error) {
	if req == nil || req.Params == nil {
		return protocol.NewToolResultError("ask_user_question arguments are required"), nil
	}
	var questionnaire jinn.Questionnaire
	if err := decodeMCPArguments(mcpAskUserTool, req.Params.Arguments, &questionnaire); err != nil {
		return protocol.NewToolResultError(err.Error()), nil
	}
	if err := jinn.ValidateQuestionnaire(questionnaire); err != nil {
		return protocol.NewToolResultError(err.Error()), nil
	}
	if len(req.Params.InputResponses) > 0 {
		response, err := req.Params.InputResponses.Elicit("questionnaire")
		if err != nil {
			return protocol.NewToolResultError(fmt.Sprintf("invalid questionnaire response: %v", err)), nil
		}
		return mcpQuestionnaireResponse(questionnaire, response)
	}
	form, _ := req.SupportsElicitation()
	if !form {
		return protocol.NewToolResultError("ask_user_question requires an MCP client with form elicitation support"), nil
	}
	return protocol.RequireInput(protocol.InputRequests{
		"questionnaire": protocol.NewElicitFormRequest("Answer the structured questions", protocol.JSONSchema(jinn.QuestionnaireElicitationSchema(questionnaire))),
	}, ""), nil
}

func mcpQuestionnaireResponse(questionnaire jinn.Questionnaire, response *protocol.ElicitResult) (protocol.ToolResponse, error) {
	if response.Action != protocol.ElicitActionAccept {
		return protocol.NewToolResultStructured(jinn.QuestionnaireResult{Answers: []jinn.QuestionnaireAnswer{}, Cancelled: true})
	}
	answers, err := jinn.QuestionnaireAnswers(questionnaire, response.Content)
	if err != nil {
		return protocol.NewToolResultError(err.Error()), nil
	}
	return protocol.NewToolResultStructured(answers)
}

func mcpCallDescription(profile mcpProfile) string {
	if profile == mcpProfileNetwork {
		return "Execute one non-mutating local or web Jinn tool. Call jinn_route first, even if the tool or arguments seem obvious. web_fetch and web_search requests leave this machine and may consume provider quota. Mutating tools and shell execution are unavailable."
	}
	return "Execute one allowlisted non-mutating Jinn tool in the current workspace. Call jinn_route first, even if the tool or arguments seem obvious. Mutating tools and shell execution are unavailable."
}

func mcpInstructions(profile mcpProfile) string {
	var values map[mcpProfile]string
	if err := json.Unmarshal(mcpInstructionsData, &values); err != nil {
		return "Call jinn_route first."
	}
	return values[profile]
}

type mcpRouteArguments struct {
	Need             string `json:"need"`
	MaxTools         *int   `json:"max_tools,omitempty"`
	IncludeSchema    bool   `json:"include_schema,omitempty"`
	IncludeSignature bool   `json:"include_signature,omitempty"`
	IncludeCall      bool   `json:"include_call,omitempty"`
	IncludeMutating  *bool  `json:"include_mutating,omitempty"`
	IncludeNetwork   *bool  `json:"include_network,omitempty"`
}
