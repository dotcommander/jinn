package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/voocel/mcp-sdk-go/protocol"
	"github.com/voocel/mcp-sdk-go/server"
	"github.com/voocel/mcp-sdk-go/transport/stdio"
)

const (
	mcpRouteTool        = "jinn_route"
	mcpCallTool         = "jinn_call"
	mcpRouteTitle       = "Call first: choose the correct development tool"
	mcpRouteDescription = "Mandatory first step whenever a development task asks which capability or tool to choose, name, recommend, or use—even when the answer seems obvious. Returns ranked exact Jinn tool names and optional input signatures; read-only and side-effect-free."
)

const mcpProbeMaxLineBytes = 16 << 20

//go:embed mcp_instructions.json
var mcpInstructionsData []byte

type mcpProfile string

const (
	mcpProfileDiscover mcpProfile = "discover"
	mcpProfileReadOnly mcpProfile = "read-only"
	mcpProfileNetwork  mcpProfile = "network"
)

func parseMCPProfile(value string) (mcpProfile, error) {
	switch mcpProfile(strings.TrimSpace(value)) {
	case mcpProfileDiscover:
		return mcpProfileDiscover, nil
	case mcpProfileReadOnly:
		return mcpProfileReadOnly, nil
	case mcpProfileNetwork:
		return mcpProfileNetwork, nil
	default:
		return "", fmt.Errorf("invalid --mcp-profile %q: use discover, read-only, or network", value)
	}
}

func openMCPProfileEngine(ldVersion string, profile mcpProfile, transportLabel string) (*jinn.Engine, error) {
	if profile != mcpProfileReadOnly && profile != mcpProfileNetwork {
		return nil, nil
	}
	workDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd for %s %s profile: %w", transportLabel, profile, err)
	}
	engine, err := jinn.NewWithConfig(workDir, jinn.EngineConfig{
		Version:   ldVersion,
		ShellMode: jinn.ShellModeDisabled,
		Web:       webConfig(),
	})
	if err != nil {
		return nil, fmt.Errorf("open %s %s workspace: %w", transportLabel, profile, err)
	}
	return engine, nil
}

// runMCP serves the current MCP 2026-07-28 protocol through the official SDK.
// A first initialize-style request on the default profile is routed to the
// legacy compatibility handler so existing jinn MCP clients can migrate
// without a flag change.
func runMCP(ctx context.Context, in io.Reader, out io.Writer, ldVersion string, mode jinn.ShellMode) error {
	return runMCPWithProfile(ctx, in, out, ldVersion, mode, mcpProfileDiscover)
}

//nolint:revive // The transport boundary keeps its six independent protocol inputs explicit.
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
	if legacy && allowsLegacyMCP(profile) {
		// Legacy initialize-based clients retain the default compatibility
		// surface. Opt-in profiles deliberately leave legacy traffic to the
		// current SDK, which rejects it before any route or tool dispatch.
		return runLegacyMCP(ctx, input, out, ldVersion, mode)
	}

	engine, err := openMCPProfileEngine(ldVersion, profile, "MCP")
	if err != nil {
		return err
	}
	if engine != nil {
		defer func() { _ = engine.Close() }()
	}

	logger, err := newMCPLoggerFromEnv()
	if err != nil {
		return err
	}
	srv := newMCPServerWithProfileAndLogger(ldVersion, mode, profile, engine, logger)
	if err := stdio.Serve(ctx, srv, &stdio.Options{Reader: input, Writer: out}); err != nil {
		return fmt.Errorf("serve MCP: %w", err)
	}
	return nil
}

func allowsLegacyMCP(profile mcpProfile) bool {
	return profile == mcpProfileDiscover
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

// detectPipedMCP lets executable-only hosts start Jinn without command-line
// arguments while preserving the existing one-shot JSON protocol. Bytes read
// during protocol selection are replayed unchanged to the selected decoder.
//
//nolint:goconst // JSON-RPC's fixed wire version is intentionally repeated across compatibility paths.
func detectPipedMCP(in io.Reader) (io.Reader, bool) {
	var captured bytes.Buffer
	limited := &io.LimitedReader{R: in, N: mcpProbeMaxLineBytes + 1}
	decoder := json.NewDecoder(io.TeeReader(limited, &captured))
	var envelope struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
	}
	decodeErr := decoder.Decode(&envelope)
	replay := io.MultiReader(bytes.NewReader(captured.Bytes()), in)
	if decodeErr != nil {
		return replay, false
	}
	return replay, envelope.JSONRPC == "2.0" && envelope.Method != ""
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

//nolint:goconst // Method and null literals are the legacy JSON-RPC wire grammar.
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

//nolint:goconst,unparam // Protocol literals are canonical; the version parameter preserves the server constructor contract.
func newMCPServer(ldVersion string, mode jinn.ShellMode) *server.Server {
	return newMCPServerWithProfile(ldVersion, mode, mcpProfileDiscover, nil)
}

// Keep the default route-only registration and the opt-in executor policy
// together for auditability.
//
//nolint:gocognit // profile-specific registration keeps the default surface and
func newMCPServerWithProfile(ldVersion string, mode jinn.ShellMode, profile mcpProfile, engine *jinn.Engine) *server.Server {
	return newMCPServerWithProfileAndLogger(ldVersion, mode, profile, engine, nil)
}

//nolint:goconst // Server identity and content-type literals are canonical MCP wire values.
func newMCPServerWithProfileAndLogger(ldVersion string, mode jinn.ShellMode, profile mcpProfile, engine *jinn.Engine, logger *mcpLogger) *server.Server {
	if ldVersion == "" {
		ldVersion = "dev"
	}
	readOnlyProfile := profile == mcpProfileReadOnly
	networkProfile := profile == mcpProfileNetwork
	routeMode := mode
	if readOnlyProfile || networkProfile {
		routeMode = jinn.ShellModeDisabled
	}
	srv := server.New(&server.Options{
		Impl: protocol.Implementation{
			Name:    "jinn",
			Title:   "Jinn",
			Version: jinn.ResolveVersion(ldVersion),
		},
		Instructions: mcpInstructions(profile),
		ListCache: protocol.CacheControl{
			TTLMs:      int64(time.Minute / time.Millisecond),
			CacheScope: protocol.CacheScopePublic,
		},
	})
	srv.Use(mcpRecoveryMiddleware(logger), mcpLoggingMiddleware(logger))
	readOnly := true
	destructive := false
	srv.AddTool(&protocol.Tool{
		Name:         mcpRouteTool,
		Title:        mcpRouteTitle,
		Description:  mcpRouteDescription,
		InputSchema:  protocol.JSONSchema(mcpRouteInputSchemaForProfile(profile)),
		OutputSchema: protocol.JSONSchema(mcpRouteOutputSchema),
		Annotations: &protocol.ToolAnnotations{
			ReadOnlyHint:    &readOnly,
			DestructiveHint: &destructive,
		},
	}, mcpRouteHandlerForProfile(routeMode, profile))
	if (readOnlyProfile || networkProfile) && engine != nil {
		srv.AddTool(&protocol.Tool{
			Name:         mcpCallTool,
			Title:        "Execute a read-only Jinn tool",
			Description:  mcpCallDescription(profile),
			InputSchema:  protocol.JSONSchema(mcpCallInputSchemaForProfile(profile)),
			OutputSchema: protocol.JSONSchema(mcpCallOutputSchema),
			Annotations: &protocol.ToolAnnotations{
				ReadOnlyHint:    &readOnly,
				DestructiveHint: &destructive,
				OpenWorldHint:   &networkProfile,
			},
		}, mcpCallHandler(engine, profile))
	}
	return srv
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

func decodeMCPArguments(label string, rawArgs map[string]any, target any) error {
	rawArgs = withoutMCPHostOnlyArguments(rawArgs)
	raw, err := json.Marshal(rawArgs)
	if err != nil {
		return fmt.Errorf("%s arguments must be a JSON object: %w", label, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%s arguments must be an object: %w", label, err)
	}
	return nil
}

// withoutMCPHostOnlyArguments keeps application arguments strict while
// removing control fields injected by agent hosts after schema discovery.
func withoutMCPHostOnlyArguments(rawArgs map[string]any) map[string]any {
	_, hasIntent := rawArgs["intent"]
	_, hasLargeOutput := rawArgs["accept_large_output"]
	if !hasIntent && !hasLargeOutput {
		return rawArgs
	}

	cleaned := make(map[string]any, len(rawArgs))
	for key, value := range rawArgs {
		cleaned[key] = value
	}
	delete(cleaned, "intent")
	delete(cleaned, "accept_large_output")
	return cleaned
}

func mcpRouteHandlerForProfile(mode jinn.ShellMode, profile mcpProfile) func(context.Context, *server.CallRequest) (protocol.ToolResponse, error) {
	return func(_ context.Context, req *server.CallRequest) (protocol.ToolResponse, error) {
		if req == nil || req.Params == nil {
			return protocol.NewToolResultError("jinn_route arguments are required"), nil
		}
		var args mcpRouteArguments
		if err := decodeMCPArguments("jinn_route", req.Params.Arguments, &args); err != nil {
			return protocol.NewToolResultError(err.Error()), nil
		}
		if strings.TrimSpace(args.Need) == "" {
			return protocol.NewToolResultError("jinn_route: 'need' is required"), nil
		}
		maxTools := 0
		if args.MaxTools != nil {
			if *args.MaxTools < 1 || *args.MaxTools > jinn.RouteMaxTools {
				return protocol.NewToolResultError(fmt.Sprintf("jinn_route: 'max_tools' must be between 1 and %d", jinn.RouteMaxTools)), nil
			}
			maxTools = *args.MaxTools
		}
		includeMutating := args.IncludeMutating
		includeNetwork := args.IncludeNetwork
		if profile == mcpProfileReadOnly || profile == mcpProfileNetwork {
			allow := false
			includeMutating = &allow
		}
		if profile == mcpProfileReadOnly {
			allow := false
			includeNetwork = &allow
		}
		route, err := jinn.RouteToolsForMode(jinn.RouteRequest{
			Need:             args.Need,
			MaxTools:         maxTools,
			IncludeSchema:    args.IncludeSchema,
			IncludeSignature: args.IncludeSignature,
			IncludeCall:      args.IncludeCall,
			IncludeMutating:  includeMutating,
			IncludeNetwork:   includeNetwork,
		}, mode)
		if err != nil {
			return protocol.NewToolResultError(err.Error()), nil
		}
		routeID, err := newMCPRouteID()
		if err != nil {
			return protocol.NewToolResultError("jinn_route: create route id"), nil
		}
		route.RouteID = routeID
		return protocol.NewToolResultStructured(route)
	}
}

type mcpCallArguments struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Compress  *bool          `json:"compress,omitempty"`
	RouteID   string         `json:"route_id,omitempty"`
}

type mcpCallOutput struct {
	Tool    string              `json:"tool"`
	Result  string              `json:"result,omitempty"`
	Content []jinn.ContentBlock `json:"content,omitempty"`
	Meta    map[string]any      `json:"meta,omitempty"`
	RouteID string              `json:"route_id,omitempty"`
}

//nolint:gocognit,gocyclo,goconst,revive // Keeping validation, allowlisting, dispatch, and projection together makes the security boundary auditable.
func mcpCallHandler(engine *jinn.Engine, profile mcpProfile) func(context.Context, *server.CallRequest) (protocol.ToolResponse, error) {
	allowed := make(map[string]struct{})
	for _, name := range jinn.ReadOnlyToolNames() {
		allowed[name] = struct{}{}
	}
	if profile == mcpProfileNetwork {
		for _, name := range jinn.NetworkToolNames() {
			allowed[name] = struct{}{}
		}
	}
	return func(ctx context.Context, req *server.CallRequest) (protocol.ToolResponse, error) {
		if req == nil || req.Params == nil {
			return protocol.NewToolResultError("jinn_call arguments are required"), nil
		}
		var args mcpCallArguments
		if err := decodeMCPArguments("jinn_call", req.Params.Arguments, &args); err != nil {
			return protocol.NewToolResultError(err.Error()), nil
		}
		args.Tool = strings.TrimSpace(args.Tool)
		if args.Tool == "" {
			return protocol.NewToolResultError("jinn_call: 'tool' is required"), nil
		}
		if _, ok := allowed[args.Tool]; !ok {
			return protocol.NewToolResultError(fmt.Sprintf("jinn_call: tool %q is unavailable in this MCP profile", args.Tool)), nil
		}
		if args.Arguments == nil {
			args.Arguments = make(map[string]any)
		}
		if !validMCPRouteID(args.RouteID) {
			return newMCPCallError(args.Tool, "", args.Arguments, &jinn.ErrWithSuggestion{
				Err:        errors.New("jinn_call: route_id must be 32 lowercase hexadecimal characters"),
				Suggestion: "use the route_id returned by jinn_route, or omit route_id",
				Code:       jinn.ErrCodeInvalidArgs,
			}), nil
		}
		result, meta, dispatchErr := engine.Dispatch(ctx, args.Tool, args.Arguments)
		if dispatchErr != nil {
			return newMCPCallError(args.Tool, args.RouteID, args.Arguments, dispatchErr), nil
		}
		compress := mcpCallCompressionEnabled(args.Tool, args.Compress)
		applyCompression(jinn.Request{Tool: args.Tool, Args: args.Arguments, Compress: compress}, result)
		output := mcpCallOutput{
			Tool:    args.Tool,
			Result:  result.Text,
			Content: result.Content,
			Meta:    mergeMCPResultMeta(result.Meta, meta),
			RouteID: args.RouteID,
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

func newMCPRouteID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func validMCPRouteID(routeID string) bool {
	if routeID == "" {
		return true
	}
	if len(routeID) != 32 || routeID != strings.ToLower(routeID) {
		return false
	}
	_, err := hex.DecodeString(routeID)
	return err == nil
}

//nolint:goconst // Tool names are the explicit compression-default contract.
func mcpCallCompressionEnabled(tool string, explicit *bool) bool {
	if explicit != nil {
		return *explicit
	}
	return tool != "web_fetch" && tool != "web_search"
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
			"description": "Maximum recommendations; omitted enables adaptive one-or-two routing.",
			"minimum":     1,
			"maximum":     jinn.RouteMaxTools,
		},
		"include_schema": map[string]any{
			"type":        "boolean",
			"description": "Include lean schemas only for returned tools.",
			"default":     false,
		},
		"include_signature": map[string]any{
			"type":        "boolean",
			"description": "Include compact input signatures only for returned tools.",
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
		"include_network": map[string]any{
			"type": "boolean", "description": "Allow recommendations for public-network tools.", "default": true,
		},
	},
	"required":             []string{"need"},
	"additionalProperties": false,
}

func mcpRouteInputSchemaForProfile(profile mcpProfile) map[string]any {
	if profile != mcpProfileReadOnly && profile != mcpProfileNetwork {
		return mcpRouteInputSchema
	}
	schema := make(map[string]any, len(mcpRouteInputSchema))
	for key, value := range mcpRouteInputSchema {
		schema[key] = value
	}
	properties := mcpRouteInputSchema["properties"].(map[string]any)
	readOnlyProperties := make(map[string]any, len(properties))
	for key, value := range properties {
		readOnlyProperties[key] = value
	}
	includeMutating := properties["include_mutating"].(map[string]any)
	readOnlyIncludeMutating := make(map[string]any, len(includeMutating))
	for key, value := range includeMutating {
		readOnlyIncludeMutating[key] = value
	}
	readOnlyIncludeMutating["default"] = false
	readOnlyIncludeMutating["description"] = "Always false in this non-mutating profile; mutating tools are never recommended."
	readOnlyProperties["include_mutating"] = readOnlyIncludeMutating
	includeNetwork := properties["include_network"].(map[string]any)
	readOnlyIncludeNetwork := make(map[string]any, len(includeNetwork))
	for key, value := range includeNetwork {
		readOnlyIncludeNetwork[key] = value
	}
	if profile == mcpProfileReadOnly {
		readOnlyIncludeNetwork["default"] = false
		readOnlyIncludeNetwork["description"] = "Always false in the read-only profile; public-network tools are never recommended."
	}
	readOnlyProperties["include_network"] = readOnlyIncludeNetwork
	schema["properties"] = readOnlyProperties
	return schema
}

//nolint:gochecknoglobals,goconst // MCP schemas are immutable package-level wire contracts.
var mcpCallInputSchema = map[string]any{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"title":   "jinn_call input",
	"type":    "object",
	"properties": map[string]any{
		"tool": map[string]any{
			"type":        "string",
			"description": "Read-only Jinn tool to execute. Call jinn_route first to choose one.",
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
		"route_id": map[string]any{
			"type":        "string",
			"description": "Route identifier returned by jinn_route.",
			"pattern":     "^[0-9a-f]{32}$",
		},
	},
	"required":             []string{"tool"},
	"additionalProperties": false,
}

func mcpCallInputSchemaForProfile(profile mcpProfile) map[string]any {
	if profile != mcpProfileNetwork {
		return mcpCallInputSchema
	}
	schema := make(map[string]any, len(mcpCallInputSchema))
	for key, value := range mcpCallInputSchema {
		schema[key] = value
	}
	properties := make(map[string]any, len(mcpCallInputSchema["properties"].(map[string]any)))
	for key, value := range mcpCallInputSchema["properties"].(map[string]any) {
		properties[key] = value
	}
	tool := make(map[string]any, len(properties["tool"].(map[string]any)))
	for key, value := range properties["tool"].(map[string]any) {
		tool[key] = value
	}
	allow := append(jinn.ReadOnlyToolNames(), jinn.NetworkToolNames()...)
	tool["enum"] = allow
	tool["description"] = "Read-only local or explicitly networked Jinn tool to execute. Call jinn_route first to choose one."
	properties["tool"] = tool
	compress := make(map[string]any, len(properties["compress"].(map[string]any)))
	for key, value := range properties["compress"].(map[string]any) {
		compress[key] = value
	}
	delete(compress, "default")
	compress["description"] = "Apply Jinn's deterministic context compression to text output. In the network profile, local read-only tools default to true and web_fetch/web_search default to false; set explicitly to override."
	properties["compress"] = compress
	schema["properties"] = properties
	return schema
}

//nolint:gochecknoglobals,goconst // MCP schemas are immutable package-level wire contracts.
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
		"route_id":   map[string]any{"type": "string"},
		"error":      map[string]any{"type": "string"},
		"error_code": map[string]any{"type": "string"},
		"suggestion": map[string]any{"type": "string"},
		"retryable":  map[string]any{"type": "boolean"},
		"next_call":  map[string]any{"$ref": "#/$defs/next_call"},
	},
	"required":             []string{"tool"},
	"additionalProperties": false,
	"$defs": map[string]any{
		"next_call": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tool":      map[string]any{"type": "string"},
				"arguments": map[string]any{"type": "object"},
			},
			"required":             []string{"tool", "arguments"},
			"additionalProperties": false,
		},
	},
}

//nolint:goconst // JSON Schema maps retain canonical wire keys beside their constraints.
var mcpRouteOutputSchema = map[string]any{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"title":   "jinn_route output",
	"type":    "object",
	"properties": map[string]any{
		"route_id": map[string]any{"type": "string"},
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
		"confidence":   map[string]any{"type": "string", "enum": []string{"none", "low", "ambiguous", "high"}},
		"score_margin": map[string]any{"type": "integer"},
		"adaptive":     map[string]any{"type": "boolean"},
	},
	"required":             []string{"query", "confidence", "adaptive", "matches", "notes"},
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
				"schema":    map[string]any{},
				"signature": map[string]any{"type": "string"},
				"call": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tool":      map[string]any{"type": "string"},
						"arguments": map[string]any{"type": "object"},
						"replace":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					},
					"required":             []string{"tool", "arguments"},
					"additionalProperties": false,
				},
			},
			"required":             []string{"name", "description", "reason", "mutating", "risk"},
			"additionalProperties": false,
		},
	},
}
