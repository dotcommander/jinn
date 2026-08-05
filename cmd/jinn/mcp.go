package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/voocel/mcp-sdk-go/protocol"
	"github.com/voocel/mcp-sdk-go/server"
	"github.com/voocel/mcp-sdk-go/transport/stdio"
)

const mcpRouteTool = "jinn_route"

const mcpCallTool = "jinn_call"

const mcpProbeMaxLineBytes = 16 << 20

type mcpProfile string

const (
	mcpProfileDiscover mcpProfile = "discover"
	mcpProfileReadOnly mcpProfile = "read-only"
)

func parseMCPProfile(value string) (mcpProfile, error) {
	switch mcpProfile(strings.TrimSpace(value)) {
	case mcpProfileDiscover:
		return mcpProfileDiscover, nil
	case mcpProfileReadOnly:
		return mcpProfileReadOnly, nil
	default:
		return "", fmt.Errorf("invalid --mcp-profile %q: use discover or read-only", value)
	}
}

// runMCP serves the current MCP 2026-07-28 protocol through the official SDK.
// A first initialize-style request is routed to the legacy compatibility
// handler so existing jinn MCP clients can migrate without a flag change.
func runMCP(ctx context.Context, in io.Reader, out io.Writer, ldVersion string, mode jinn.ShellMode) error {
	return runMCPWithProfile(ctx, in, out, ldVersion, mode, mcpProfileDiscover)
}

func runMCPWithProfile(ctx context.Context, in io.Reader, out io.Writer, ldVersion string, mode jinn.ShellMode, profile mcpProfile) error {
	if profile == "" {
		profile = mcpProfileDiscover
	}
	reader := bufio.NewReader(in)
	prefix, legacy, err := readMCPPrefix(reader)
	if err != nil {
		return fmt.Errorf("read MCP input: %w", err)
	}
	input := io.MultiReader(bytes.NewReader(prefix), reader)
	if legacy {
		// Legacy initialize-based clients retain the compatibility surface. The
		// read-only profile requires the current stateless request metadata.
		return runLegacyMCP(ctx, input, out, ldVersion, mode)
	}

	var engine *jinn.Engine
	if profile == mcpProfileReadOnly {
		workDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd for MCP read-only profile: %w", err)
		}
		engine, err = jinn.NewWithConfig(workDir, jinn.EngineConfig{
			Version:   ldVersion,
			ShellMode: jinn.ShellModeDisabled,
		})
		if err != nil {
			return fmt.Errorf("open MCP read-only workspace: %w", err)
		}
		defer func() { _ = engine.Close() }()
	}

	srv := newMCPServerWithProfile(ldVersion, mode, profile, engine)
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
	return newMCPServerWithProfile(ldVersion, mode, mcpProfileDiscover, nil)
}

// the opt-in executor policy close together for auditability.
//
//nolint:gocognit // profile-specific registration keeps the default surface and
func newMCPServerWithProfile(ldVersion string, mode jinn.ShellMode, profile mcpProfile, engine *jinn.Engine) *server.Server {
	if ldVersion == "" {
		ldVersion = "dev"
	}
	readOnlyProfile := profile == mcpProfileReadOnly
	routeMode := mode
	if readOnlyProfile {
		routeMode = jinn.ShellModeDisabled
	}
	srv := server.New(&server.Options{
		Impl: protocol.Implementation{
			Name:    "jinn",
			Title:   "Jinn",
			Version: jinn.ResolveVersion(ldVersion),
		},
		Instructions: mcpInstructions(profile),
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
	}, mcpRouteHandlerForProfile(routeMode, profile))
	if readOnlyProfile && engine != nil {
		srv.AddTool(&protocol.Tool{
			Name:         mcpCallTool,
			Title:        "Execute a read-only Jinn tool",
			Description:  "Execute one allowlisted read-only Jinn tool in the current workspace. Use jinn_route first when the tool or arguments are uncertain. Mutating tools and shell execution are unavailable in this profile.",
			InputSchema:  protocol.JSONSchema(mcpCallInputSchema),
			OutputSchema: protocol.JSONSchema(mcpCallOutputSchema),
			Annotations: &protocol.ToolAnnotations{
				ReadOnlyHint:    &readOnly,
				DestructiveHint: &destructive,
			},
		}, mcpCallHandler(engine))
	}
	return srv
}

func mcpInstructions(profile mcpProfile) string {
	if profile == mcpProfileReadOnly {
		return "Use jinn_route to deterministically find relevant Jinn tools. This opt-in read-only profile also exposes jinn_call for the canonical read-only allowlist; it never permits file or state mutation and never executes shell commands."
	}
	return "Use jinn_route to deterministically find relevant Jinn tools. The MCP surface intentionally exposes one recommendation-only tool to keep model context small; it never executes tools."
}

type mcpRouteArguments struct {
	Need            string `json:"need"`
	MaxTools        int    `json:"max_tools,omitempty"`
	IncludeSchema   bool   `json:"include_schema,omitempty"`
	IncludeMutating *bool  `json:"include_mutating,omitempty"`
}

func mcpRouteHandlerForMode(mode jinn.ShellMode) func(context.Context, *server.CallRequest) (protocol.ToolResponse, error) {
	return mcpRouteHandlerForProfile(mode, mcpProfileDiscover)
}

func mcpRouteHandlerForProfile(mode jinn.ShellMode, profile mcpProfile) func(context.Context, *server.CallRequest) (protocol.ToolResponse, error) {
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
		includeMutating := args.IncludeMutating
		if profile == mcpProfileReadOnly {
			allow := false
			includeMutating = &allow
		}
		route, err := jinn.RouteToolsForMode(jinn.RouteRequest{
			Need:            args.Need,
			MaxTools:        args.MaxTools,
			IncludeSchema:   args.IncludeSchema,
			IncludeMutating: includeMutating,
		}, mode)
		if err != nil {
			return protocol.NewToolResultError(err.Error()), nil
		}
		return protocol.NewToolResultStructured(route)
	}
}

type mcpCallArguments struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Compress  *bool          `json:"compress,omitempty"`
}

type mcpCallOutput struct {
	Tool    string              `json:"tool"`
	Result  string              `json:"result,omitempty"`
	Content []jinn.ContentBlock `json:"content,omitempty"`
	Meta    map[string]any      `json:"meta,omitempty"`
}

func mcpCallHandler(engine *jinn.Engine) func(context.Context, *server.CallRequest) (protocol.ToolResponse, error) {
	allowed := make(map[string]struct{})
	for _, name := range jinn.ReadOnlyToolNames() {
		allowed[name] = struct{}{}
	}
	return func(ctx context.Context, req *server.CallRequest) (protocol.ToolResponse, error) {
		if req == nil || req.Params == nil {
			return protocol.NewToolResultError("jinn_call arguments are required"), nil
		}
		var args mcpCallArguments
		raw, err := json.Marshal(req.Params.Arguments)
		if err != nil {
			return protocol.NewToolResultError(fmt.Sprintf("jinn_call arguments must be a JSON object: %v", err)), nil
		}
		if unmarshalErr := json.Unmarshal(raw, &args); unmarshalErr != nil {
			return protocol.NewToolResultError(fmt.Sprintf("jinn_call arguments must be an object: %v", unmarshalErr)), nil
		}
		args.Tool = strings.TrimSpace(args.Tool)
		if args.Tool == "" {
			return protocol.NewToolResultError("jinn_call: 'tool' is required"), nil
		}
		if _, ok := allowed[args.Tool]; !ok {
			return protocol.NewToolResultError(fmt.Sprintf("jinn_call: tool %q is unavailable in the read-only profile", args.Tool)), nil
		}
		if args.Arguments == nil {
			args.Arguments = make(map[string]any)
		}
		result, meta, dispatchErr := engine.Dispatch(ctx, args.Tool, args.Arguments)
		if dispatchErr != nil {
			return protocol.NewToolResultError(dispatchErr.Error()), nil
		}
		compress := args.Compress == nil || *args.Compress
		applyCompression(jinn.Request{Tool: args.Tool, Args: args.Arguments, Compress: compress}, result)
		output := mcpCallOutput{
			Tool:    args.Tool,
			Result:  result.Text,
			Content: result.Content,
			Meta:    mergeMCPResultMeta(result.Meta, meta),
		}
		response, responseErr := protocol.NewToolResultStructured(output)
		if responseErr != nil {
			return protocol.NewToolResultError(fmt.Sprintf("jinn_call: encode result: %v", responseErr)), nil
		}
		for _, block := range result.Content {
			switch block.Type {
			case "text":
				response.Content = append(response.Content, protocol.NewTextContent(block.Text))
			case "image":
				response.Content = append(response.Content, protocol.NewImageContent(block.Data, block.MimeType))
			}
		}
		return response, nil
	}
}

func mergeMCPResultMeta(resultMeta, dispatchMeta map[string]any) map[string]any {
	if len(resultMeta) == 0 && len(dispatchMeta) == 0 {
		return nil
	}
	merged := make(map[string]any, len(resultMeta)+len(dispatchMeta))
	for key, value := range resultMeta {
		merged[key] = value
	}
	for key, value := range dispatchMeta {
		merged[key] = value
	}
	return merged
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

//nolint:gochecknoglobals // MCP schemas are immutable package-level wire contracts.
var mcpCallInputSchema = map[string]any{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"title":   "jinn_call input",
	"type":    "object",
	"properties": map[string]any{
		"tool": map[string]any{
			"type":        "string",
			"description": "Read-only Jinn tool to execute. Use jinn_route to choose one.",
			"enum":        jinn.ReadOnlyToolNames(),
		},
		"arguments": map[string]any{
			"type":                 "object",
			"description":          "Arguments for the selected read-only Jinn tool.",
			"additionalProperties": true,
		},
		"compress": map[string]any{
			"type":        "boolean",
			"description": "Apply Jinn's deterministic context compression to text output.",
			"default":     true,
		},
	},
	"required":             []string{"tool"},
	"additionalProperties": false,
}

//nolint:gochecknoglobals // MCP schemas are immutable package-level wire contracts.
var mcpCallOutputSchema = map[string]any{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"title":   "jinn_call output",
	"type":    "object",
	"properties": map[string]any{
		"tool": map[string]any{
			"type": "string",
		},
		"result": map[string]any{
			"type": "string",
		},
		"content": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":     map[string]any{"type": "string"},
					"text":     map[string]any{"type": "string"},
					"data":     map[string]any{"type": "string"},
					"mimeType": map[string]any{"type": "string"},
				},
				"required":             []string{"type"},
				"additionalProperties": false,
			},
		},
		"meta": map[string]any{
			"type":                 "object",
			"additionalProperties": true,
		},
	},
	"required":             []string{"tool"},
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
