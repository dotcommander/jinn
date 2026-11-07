package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
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
	mcpAskUserTool      = "ask_user_question"
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
// Only clients explicitly requesting the legacy protocol on the default
// profile are routed to the compatibility handler.
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
		// Explicit legacy-version clients retain the compatibility surface.
		// Opt-in profiles deliberately leave legacy traffic to the
		// current SDK, which rejects it before any route or tool dispatch.
		return runLegacyMCP(ctx, input, out, ldVersion, mode)
	}
	// The current SDK uses server/discover rather than initialize. Answer a
	// current-version initialize on this compatibility seam, then pass every
	// subsequent message through the current SDK unchanged.
	//nolint:nestif // nested guards express one validation/state ladder
	if line, remaining, ok := currentMCPInitializePrefix(prefix); ok && (profile == mcpProfileDiscover || !isLegacyMCPLine(line)) {
		resp, respond := handleMCPLine(line, ldVersion, mode)
		if respond {
			if result, ok := resp.Result.(map[string]any); ok {
				result["protocolVersion"] = protocol.Version
			}
			if encodeErr := json.NewEncoder(out).Encode(resp); encodeErr != nil {
				return fmt.Errorf("write MCP initialize: %w", encodeErr)
			}
		}
		input = io.MultiReader(bytes.NewReader(remaining), reader)
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

// Keep the default compact routing/elicitation registration and opt-in executor policy
// together for auditability.
//
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
	srv.AddTool(&protocol.Tool{
		Name:        mcpAskUserTool,
		Title:       "Ask the user structured questions",
		Description: "Ask 1-4 structured questions through MCP form elicitation instead of guessing. Requires an elicitation-capable client.",
		InputSchema: protocol.JSONSchema(mcpAskUserInputSchema),
		Annotations: &protocol.ToolAnnotations{ReadOnlyHint: &readOnly, DestructiveHint: &destructive},
	}, mcpAskUserHandler)
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

//nolint:goconst // Questionnaire schema keys are intentionally explicit for MCP clients.
var mcpAskUserInputSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"questions": map[string]any{
			"type": "array", "minItems": 1, "maxItems": jinn.MaxQuestions,
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question":    map[string]any{"type": "string", "minLength": 1},
					"header":      map[string]any{"type": "string", "minLength": 1, "maxLength": jinn.MaxHeaderLength},
					"options":     map[string]any{"type": "array", "minItems": jinn.MinOptions, "maxItems": jinn.MaxOptions, "items": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string", "minLength": 1}, "label": map[string]any{"type": "string", "maxLength": jinn.MaxLabelLength}, "description": map[string]any{"type": "string", "minLength": 1}, "recommended": map[string]any{"type": "boolean"}}, "required": []string{"value", "label", "description"}, "additionalProperties": false}},
					"multiSelect": map[string]any{"type": "boolean"},
				},
				"required": []string{"question", "header", "options"}, "additionalProperties": false,
			},
		},
	},
	"required": []string{"questions"}, "additionalProperties": false,
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

//nolint:gocognit,revive // branchy by design; splitting would scatter one sequential algorithm
