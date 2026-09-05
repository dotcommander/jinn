package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/dotcommander/jinn/internal/jinn"
)

const (
	mcpProtocolVersion  = "2025-06-18"
	mcpMethodInitialize = "initialize"
	mcpMethodToolsCall  = "tools/call"
	mcpResultType       = "type"
	mcpResultContent    = "content"
	mcpResultText       = "text"
	mcpResultIsError    = "isError"
)

type mcpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  any             `json:"params"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpError       `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type legacyMCPRunner struct {
	scanner                   *bufio.Scanner
	encoder                   *json.Encoder
	ldVersion                 string
	mode                      jinn.ShellMode
	clientSupportsElicitation bool
	elicitationSequence       int
}

func runLegacyMCP(ctx context.Context, in io.Reader, out io.Writer, ldVersion string, mode jinn.ShellMode) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	runner := legacyMCPRunner{scanner: scanner, encoder: json.NewEncoder(out), ldVersion: ldVersion, mode: mode}
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		resp, respond := runner.handleLine(ctx, []byte(line))
		if !respond {
			continue
		}
		if err := runner.encoder.Encode(resp); err != nil {
			return fmt.Errorf("write mcp response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read mcp stdin: %w", err)
	}
	return nil
}

func (r *legacyMCPRunner) handleLine(ctx context.Context, line []byte) (mcpResponse, bool) {
	var msg mcpMessage
	if err := json.Unmarshal(line, &msg); err == nil {
		if msg.Method == mcpMethodInitialize {
			r.clientSupportsElicitation = legacyMCPClientSupportsElicitation(msg.Params)
		}
		if msg.Method == mcpMethodToolsCall && legacyMCPToolName(msg.Params) == mcpAskUserTool {
			r.elicitationSequence++
			return r.handleAskUser(ctx, msg), true
		}
	}
	return handleMCPLine(line, r.ldVersion, r.mode)
}

//nolint:goconst // legacy JSON-RPC payloads intentionally retain canonical wire literals.
func handleMCPLine(line []byte, ldVersion string, mode jinn.ShellMode) (mcpResponse, bool) {
	var msg mcpMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return mcpResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &mcpError{Code: -32700, Message: "Parse error", Data: err.Error()},
		}, true
	}
	if len(msg.ID) == 0 {
		handleMCPNotification(msg)
		return mcpResponse{}, false
	}
	switch msg.Method {
	case mcpMethodInitialize:
		return mcpResult(msg.ID, map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"instructions":    mcpInstructions(mcpProfileDiscover),
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name":    "jinn",
				"title":   "Jinn",
				"version": jinn.ResolveVersion(ldVersion),
			},
		}), true
	case "ping":
		return mcpResult(msg.ID, map[string]any{}), true
	case "tools/list":
		return mcpResult(msg.ID, map[string]any{"tools": []any{mcpRouteToolDefinition(), mcpAskUserToolDefinition()}}), true
	case mcpMethodToolsCall:
		result, err := handleMCPToolCall(msg.Params, mode)
		if err != nil {
			return mcpProtocolError(msg.ID, -32602, err.Error(), nil), true
		}
		return mcpResult(msg.ID, result), true
	default:
		return mcpProtocolError(msg.ID, -32601, "Method not found", map[string]string{"method": msg.Method}), true
	}
}

func legacyMCPClientSupportsElicitation(params json.RawMessage) bool {
	var initialize struct {
		Capabilities struct {
			Elicitation struct {
				Form map[string]any `json:"form"`
			} `json:"elicitation"`
		} `json:"capabilities"`
	}
	return json.Unmarshal(params, &initialize) == nil && initialize.Capabilities.Elicitation.Form != nil
}

func legacyMCPToolName(params json.RawMessage) string {
	var call mcpToolCallParams
	if json.Unmarshal(params, &call) != nil {
		return ""
	}
	return call.Name
}

func (r *legacyMCPRunner) handleAskUser(ctx context.Context, msg mcpMessage) mcpResponse {
	if !r.clientSupportsElicitation {
		return mcpProtocolError(msg.ID, -32602, "ask_user_question requires an MCP client with form elicitation support", nil)
	}
	var call mcpToolCallParams
	if err := json.Unmarshal(msg.Params, &call); err != nil {
		return mcpProtocolError(msg.ID, -32602, fmt.Sprintf("invalid tools/call params: %v", err), nil)
	}
	questionnaire, err := jinn.DecodeQuestionnaire(call.Arguments)
	if err != nil {
		return mcpProtocolError(msg.ID, -32602, err.Error(), nil)
	}

	requestID := json.RawMessage(fmt.Sprintf("%q", fmt.Sprintf("jinn-elicitation-%d", r.elicitationSequence)))
	request := mcpRequest{
		JSONRPC: "2.0",
		ID:      requestID,
		Method:  "elicitation/create",
		Params: map[string]any{
			"mode":            "form",
			"message":         "Answer the structured questions",
			"requestedSchema": jinn.QuestionnaireElicitationSchema(questionnaire),
		},
	}
	if writeErr := r.encoder.Encode(request); writeErr != nil {
		return mcpProtocolError(msg.ID, -32603, fmt.Sprintf("write elicitation request: %v", writeErr), nil)
	}

	result, err := readLegacyMCPElicitationResult(ctx, r.scanner, requestID)
	if err != nil {
		return mcpProtocolError(msg.ID, -32603, err.Error(), nil)
	}
	return mcpResult(msg.ID, legacyMCPQuestionnaireToolResult(questionnaire, result))
}

func readLegacyMCPElicitationResult(ctx context.Context, scanner *bufio.Scanner, requestID json.RawMessage) (map[string]any, error) {
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		var msg mcpMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			return nil, fmt.Errorf("decode elicitation response: %w", err)
		}
		if string(msg.ID) != string(requestID) || msg.Method != "" {
			continue
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("elicitation failed: %s", msg.Error.Message)
		}
		var result map[string]any
		if err := json.Unmarshal(msg.Result, &result); err != nil {
			return nil, fmt.Errorf("decode elicitation result: %w", err)
		}
		return result, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read elicitation response: %w", err)
	}
	return nil, errors.New("MCP client closed before returning elicitation input")
}

func legacyMCPQuestionnaireToolResult(questionnaire jinn.Questionnaire, elicitation map[string]any) map[string]any {
	action, _ := elicitation["action"].(string)
	result := jinn.QuestionnaireResult{Answers: []jinn.QuestionnaireAnswer{}, Cancelled: true}
	if action == "accept" {
		content, ok := elicitation[mcpResultContent].(map[string]any)
		if !ok {
			return legacyMCPToolError("elicitation accepted without questionnaire content")
		}
		answers, err := jinn.QuestionnaireAnswers(questionnaire, content)
		if err != nil {
			return legacyMCPToolError(err.Error())
		}
		result = answers
	}
	data, err := json.Marshal(result)
	if err != nil {
		return legacyMCPToolError(fmt.Sprintf("marshal questionnaire result: %v", err))
	}
	return map[string]any{
		mcpResultContent:    []map[string]string{{mcpResultType: mcpResultText, mcpResultText: string(data)}},
		"structuredContent": result,
		mcpResultIsError:    false,
	}
}

func legacyMCPToolError(message string) map[string]any {
	return map[string]any{
		mcpResultContent: []map[string]string{{mcpResultType: mcpResultText, mcpResultText: message}},
		mcpResultIsError: true,
	}
}

func handleMCPNotification(msg mcpMessage) {
	// notifications/initialized is intentionally a no-op. Unknown
	// notifications also produce no response per JSON-RPC notification rules.
	_ = msg
}

//nolint:goconst // MCP content maps intentionally retain canonical response keys.
func handleMCPToolCall(params json.RawMessage, mode jinn.ShellMode) (any, error) {
	var call mcpToolCallParams
	if len(params) == 0 {
		return nil, errors.New("missing tools/call params")
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, fmt.Errorf("invalid tools/call params: %w", err)
	}
	if call.Name != mcpRouteTool {
		return nil, fmt.Errorf("unknown MCP tool: %s", call.Name)
	}
	if len(call.Arguments) == 0 || string(call.Arguments) == "null" {
		return nil, errors.New("missing jinn_route arguments")
	}
	req, err := decodeLegacyMCPRouteRequest(call.Arguments)
	if err != nil {
		return nil, fmt.Errorf("invalid jinn_route arguments: %w", err)
	}
	if strings.TrimSpace(req.Need) == "" {
		return nil, errors.New("jinn_route need is required")
	}
	route, err := jinn.RouteToolsForMode(req, mode)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(route)
	if err != nil {
		return nil, fmt.Errorf("marshal route: %w", err)
	}
	return map[string]any{
		"content": []map[string]string{
			{mcpResultType: mcpResultText, mcpResultText: string(data)},
		},
		"isError": false,
	}, nil
}

func decodeLegacyMCPRouteRequest(rawArgs json.RawMessage) (jinn.RouteRequest, error) {
	req, err := jinn.DecodeRouteRequest(rawArgs)
	if err == nil || !isMCPHostOnlyUnknownField(err) {
		return req, err
	}

	var arguments map[string]any
	if unmarshalErr := json.Unmarshal(rawArgs, &arguments); unmarshalErr != nil {
		return jinn.RouteRequest{}, unmarshalErr
	}
	cleaned, marshalErr := json.Marshal(withoutMCPHostOnlyArguments(arguments))
	if marshalErr != nil {
		return jinn.RouteRequest{}, marshalErr
	}
	return jinn.DecodeRouteRequest(cleaned)
}

func isMCPHostOnlyUnknownField(err error) bool {
	return err.Error() == `json: unknown field "intent"` ||
		err.Error() == `json: unknown field "accept_large_output"`
}

func mcpResult(id json.RawMessage, result any) mcpResponse {
	return mcpResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func mcpProtocolError(id json.RawMessage, code int, message string, data any) mcpResponse {
	return mcpResponse{JSONRPC: "2.0", ID: id, Error: &mcpError{Code: code, Message: message, Data: data}}
}

//nolint:goconst // JSON Schema map preserves canonical wire keys beside their values.
func mcpRouteToolDefinition() map[string]any {
	return map[string]any{
		"name":        "jinn_route",
		"title":       mcpRouteTitle,
		"description": mcpRouteDescription,
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"need": map[string]any{
					"type":        "string",
					"description": "Natural-language task or capability request.",
				},
				"max_tools": map[string]any{
					"type":        "integer",
					"description": "Maximum recommendations; omit for adaptive routing.",
					"maximum":     jinn.RouteMaxTools,
				},
				"include_schema": map[string]any{
					"type":        "boolean",
					"description": "Include lean schemas only for returned tools.",
					"default":     false,
				},
				"include_signature": map[string]any{
					"type":        "boolean",
					"description": "Include compact input signatures.",
					"default":     false,
				},
				"include_call": map[string]any{
					"type":        "boolean",
					"description": "Include one minimal argument template.",
					"default":     false,
				},
				"include_mutating": map[string]any{
					"type":        "boolean",
					"description": "Allow recommendations for mutating tools.",
					"default":     true,
				},
			},
			"required": []string{"need"},
		},
	}
}

func mcpAskUserToolDefinition() map[string]any {
	return map[string]any{
		"name":        mcpAskUserTool,
		"title":       "Ask the user structured questions",
		"description": "Ask 1-4 structured questions through MCP form elicitation instead of guessing. Requires an elicitation-capable client.",
		"inputSchema": mcpAskUserInputSchema,
		"annotations": map[string]any{"readOnlyHint": true, "destructiveHint": false},
	}
}
