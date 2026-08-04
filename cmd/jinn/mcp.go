package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/voocel/mcp-sdk-go/protocol"
	"github.com/voocel/mcp-sdk-go/server"
	"github.com/voocel/mcp-sdk-go/transport/stdio"
)

const mcpRouteTool = "jinn_route"

const mcpProbeMaxLineBytes = 16 << 20

// runMCP serves the current MCP 2026-07-28 protocol through the official SDK.
// A first initialize-style request is routed to the legacy compatibility
// handler so existing jinn MCP clients can migrate without a flag change.
func runMCP(ctx context.Context, in io.Reader, out io.Writer, ldVersion string, mode jinn.ShellMode) error {
	reader := bufio.NewReader(in)
	prefix, legacy, err := readMCPPrefix(reader)
	if err != nil {
		return fmt.Errorf("read MCP input: %w", err)
	}
	input := io.MultiReader(bytes.NewReader(prefix), reader)
	if legacy {
		return runLegacyMCP(ctx, input, out, ldVersion, mode)
	}

	srv := newMCPServer(ldVersion, mode)
	if err := stdio.Serve(ctx, srv, &stdio.Options{Reader: input, Writer: out}); err != nil {
		return fmt.Errorf("serve MCP: %w", err)
	}
	return nil
}

func readMCPPrefix(reader *bufio.Reader) ([]byte, bool, error) {
	var prefix bytes.Buffer
	for {
		line, err := readMCPProbeLine(reader)
		if line != "" {
			_, _ = prefix.WriteString(line)
		}
		if strings.TrimSpace(line) != "" {
			return prefix.Bytes(), isLegacyMCPLine([]byte(line)), nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return prefix.Bytes(), false, nil
			}
			return prefix.Bytes(), false, err
		}
	}
}

func readMCPProbeLine(reader *bufio.Reader) (string, error) {
	var line []byte
	for {
		fragment, isPrefix, err := reader.ReadLine()
		line = append(line, fragment...)
		if len(line) > mcpProbeMaxLineBytes {
			return "", fmt.Errorf("MCP request exceeds %d bytes", mcpProbeMaxLineBytes)
		}
		if err != nil {
			return string(line), err
		}
		if !isPrefix {
			line = append(line, '\n')
			return string(line), nil
		}
	}
}

func isLegacyMCPLine(line []byte) bool {
	var probe struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return false
	}
	if probe.Method == "initialize" {
		return true
	}
	switch probe.Method {
	case "ping", "tools/list", "tools/call", "resources/list":
		var params map[string]any
		if len(probe.Params) == 0 || string(probe.Params) == "null" {
			return true
		}
		if err := json.Unmarshal(probe.Params, &params); err != nil {
			return true
		}
		_, hasMeta := params["_meta"]
		return !hasMeta
	default:
		return false
	}
}

//nolint:goconst // protocol metadata uses canonical JSON-RPC and JSON Schema wire literals.
func newMCPServer(ldVersion string, mode jinn.ShellMode) *server.Server {
	if ldVersion == "" {
		ldVersion = "dev"
	}
	srv := server.New(&server.Options{
		Impl: protocol.Implementation{
			Name:    "jinn",
			Title:   "Jinn",
			Version: jinn.ResolveVersion(ldVersion),
		},
		Instructions: "Use jinn_route to deterministically find relevant Jinn tools. The MCP surface intentionally exposes one recommendation-only tool to keep model context small; it never executes tools.",
	})
	readOnly := true
	destructive := false
	srv.AddTool(&protocol.Tool{
		Name:         mcpRouteTool,
		Title:        "Route to Jinn tools",
		Description:  "Deterministically find relevant Jinn tools for a coding-agent task. Recommendation only; does not execute tools.",
		InputSchema:  protocol.JSONSchema(mcpRouteInputSchema),
		OutputSchema: protocol.JSONSchema(mcpRouteOutputSchema),
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint:    &readOnly,
			DestructiveHint: &destructive,
		},
	}, mcpRouteHandlerForMode(mode))
	return srv
}

type mcpRouteArguments struct {
	Need            string `json:"need"`
	MaxTools        int    `json:"max_tools,omitempty"`
	IncludeSchema   bool   `json:"include_schema,omitempty"`
	IncludeMutating *bool  `json:"include_mutating,omitempty"`
}

func mcpRouteHandlerForMode(mode jinn.ShellMode) func(context.Context, *server.CallRequest) (protocol.ToolResponse, error) {
	return func(_ context.Context, req *server.CallRequest) (protocol.ToolResponse, error) {
		if req == nil || req.Params == nil {
			return protocol.NewToolResultError("jinn_route arguments are required"), nil
		}
		var args mcpRouteArguments
		raw, err := json.Marshal(req.Params.Arguments)
		if err != nil {
			return protocol.NewToolResultError(fmt.Sprintf("jinn_route arguments must be a JSON object: %v", err)), nil
		}
		if unmarshalErr := json.Unmarshal(raw, &args); unmarshalErr != nil {
			return protocol.NewToolResultError(fmt.Sprintf("jinn_route arguments must be an object: %v", unmarshalErr)), nil
		}
		if strings.TrimSpace(args.Need) == "" {
			return protocol.NewToolResultError("jinn_route: 'need' is required"), nil
		}
		route, err := jinn.RouteToolsForMode(jinn.RouteRequest{
			Need:            args.Need,
			MaxTools:        args.MaxTools,
			IncludeSchema:   args.IncludeSchema,
			IncludeMutating: args.IncludeMutating,
		}, mode)
		if err != nil {
			return protocol.NewToolResultError(err.Error()), nil
		}
		return protocol.NewToolResultStructured(route)
	}
}

//nolint:goconst // JSON Schema maps retain canonical wire keys beside their constraints.
var mcpRouteInputSchema = map[string]any{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"title":   "jinn_route input",
	"type":    "object",
	"properties": map[string]any{
		"need": map[string]any{
			"type":        "string",
			"description": "Concrete natural-language coding task or capability request.",
		},
		"max_tools": map[string]any{
			"type":        "integer",
			"description": "Maximum recommendations to return. Defaults to 5 and is capped at 8.",
			"minimum":     1,
			"maximum":     jinn.RouteMaxTools,
			"default":     jinn.RouteDefaultMaxTools,
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
	"required":             []string{"need"},
	"additionalProperties": false,
}

//nolint:goconst // JSON Schema maps retain canonical wire keys beside their constraints.
var mcpRouteOutputSchema = map[string]any{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"title":   "jinn_route output",
	"type":    "object",
	"properties": map[string]any{
		"query": map[string]any{
			"type": "string",
		},
		"matches": map[string]any{
			"type":  "array",
			"items": map[string]any{"$ref": "#/$defs/route_match"},
		},
		"notes": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
		},
	},
	"required":             []string{"query", "matches", "notes"},
	"additionalProperties": false,
	"$defs": map[string]any{
		"route_match": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"reason":      map[string]any{"type": "string"},
				"mutating":    map[string]any{"type": "boolean"},
				"risk":        map[string]any{"type": "string"},
				"features": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"schema": map[string]any{},
			},
			"required":             []string{"name", "description", "reason", "mutating", "risk"},
			"additionalProperties": false,
		},
	},
}
