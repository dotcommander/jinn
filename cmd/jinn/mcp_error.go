package main

import (
	"path/filepath"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/voocel/mcp-sdk-go/protocol"
)

type mcpCallErrorOutput struct {
	Tool       string         `json:"tool"`
	RouteID    string         `json:"route_id,omitempty"`
	Error      string         `json:"error"`
	ErrorCode  string         `json:"error_code"`
	Suggestion string         `json:"suggestion,omitempty"`
	Retryable  bool           `json:"retryable"`
	NextCall   *jinn.NextCall `json:"next_call,omitempty"`
}

func newMCPCallError(tool, routeID string, args map[string]any, err error) *protocol.CallToolResult {
	code, suggestion := jinn.ErrorDetails(err)
	if code == "" {
		code = "tool_error"
	}
	output := mcpCallErrorOutput{
		Tool: tool, RouteID: routeID, Error: err.Error(), ErrorCode: code,
		Suggestion: suggestion, Retryable: mcpErrorRetryable(code),
		NextCall: mcpRecoveryCall(tool, args, code),
	}
	result, encodeErr := protocol.NewToolResultStructured(output)
	if encodeErr != nil {
		return protocol.NewToolResultError(err.Error())
	}
	result.IsError = true
	return result
}

func mcpErrorRetryable(code string) bool {
	switch code {
	case jinn.ErrCodePermissionDenied, jinn.ErrCodePathOutsideSandbox, jinn.ErrCodeLspUnavailable:
		return false
	default:
		return true
	}
}

func mcpRecoveryCall(tool string, args map[string]any, code string) *jinn.NextCall {
	switch code {
	case jinn.ErrCodeInvalidRegex:
		if tool != "search_files" {
			return nil
		}
		arguments := cloneMCPArguments(args)
		arguments["literal"] = true
		return &jinn.NextCall{Tool: tool, Arguments: arguments}
	case jinn.ErrCodeFileNotFound:
		path, _ := args["path"].(string)
		if path == "" {
			return nil
		}
		return &jinn.NextCall{Tool: "list_dir", Arguments: map[string]any{"path": filepath.Dir(path), "depth": 1}}
	case jinn.ErrCodeInvalidArgs:
		if tool != "lsp_query" {
			return nil
		}
		path, _ := args["path"].(string)
		symbol, _ := args["symbol"].(string)
		if path == "" || symbol == "" {
			return nil
		}
		return &jinn.NextCall{Tool: "lsp_query", Arguments: map[string]any{"action": "symbols", "path": path}}
	default:
		return nil
	}
}

func cloneMCPArguments(args map[string]any) map[string]any {
	result := make(map[string]any, len(args))
	for key, value := range args {
		result[key] = value
	}
	return result
}
