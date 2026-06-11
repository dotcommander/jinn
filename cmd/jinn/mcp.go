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

const mcpProtocolVersion = "2025-06-18"

type mcpMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
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

func runMCP(ctx context.Context, in io.Reader, out io.Writer, ldVersion string) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	enc := json.NewEncoder(out)
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
		resp, respond := handleMCPLine([]byte(line), ldVersion)
		if !respond {
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("write mcp response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read mcp stdin: %w", err)
	}
	return nil
}

func handleMCPLine(line []byte, ldVersion string) (mcpResponse, bool) {
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
	case "initialize":
		return mcpResult(msg.ID, map[string]any{
			"protocolVersion": mcpProtocolVersion,
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
		return mcpResult(msg.ID, map[string]any{"tools": []any{mcpRouteToolDefinition()}}), true
	case "tools/call":
		result, err := handleMCPToolCall(msg.Params)
		if err != nil {
			return mcpProtocolError(msg.ID, -32602, err.Error(), nil), true
		}
		return mcpResult(msg.ID, result), true
	default:
		return mcpProtocolError(msg.ID, -32601, "Method not found", map[string]string{"method": msg.Method}), true
	}
}

func handleMCPNotification(msg mcpMessage) {
	// notifications/initialized is intentionally a no-op. Unknown
	// notifications also produce no response per JSON-RPC notification rules.
	_ = msg
}

func handleMCPToolCall(params json.RawMessage) (any, error) {
	var call mcpToolCallParams
	if len(params) == 0 {
		return nil, errors.New("missing tools/call params")
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return nil, fmt.Errorf("invalid tools/call params: %w", err)
	}
	if call.Name != "jinn_route" {
		return nil, fmt.Errorf("unknown MCP tool: %s", call.Name)
	}
	if len(call.Arguments) == 0 || string(call.Arguments) == "null" {
		return nil, errors.New("missing jinn_route arguments")
	}
	req, err := jinn.DecodeRouteRequest(call.Arguments)
	if err != nil {
		return nil, fmt.Errorf("invalid jinn_route arguments: %w", err)
	}
	if strings.TrimSpace(req.Need) == "" {
		return nil, errors.New("jinn_route need is required")
	}
	route, err := jinn.RouteTools(req)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(route)
	if err != nil {
		return nil, fmt.Errorf("marshal route: %w", err)
	}
	return map[string]any{
		"content": []map[string]string{
			{"type": "text", "text": string(data)},
		},
		"isError": false,
	}, nil
}

func mcpResult(id json.RawMessage, result any) mcpResponse {
	return mcpResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func mcpProtocolError(id json.RawMessage, code int, message string, data any) mcpResponse {
	return mcpResponse{JSONRPC: "2.0", ID: id, Error: &mcpError{Code: code, Message: message, Data: data}}
}

func mcpRouteToolDefinition() map[string]any {
	return map[string]any{
		"name":        "jinn_route",
		"title":       "Jinn Tool Router",
		"description": "Find relevant jinn tools for a coding-agent task. Recommendation only; does not execute tools.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"need": map[string]any{
					"type":        "string",
					"description": "Natural-language task or capability request.",
				},
				"max_tools": map[string]any{
					"type":        "integer",
					"description": "Maximum recommendations to return.",
					"default":     jinn.RouteDefaultMaxTools,
					"maximum":     jinn.RouteMaxTools,
				},
				"include_schema": map[string]any{
					"type":        "boolean",
					"description": "Include lean schemas only for returned tools.",
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
