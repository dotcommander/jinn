package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/voocel/mcp-sdk-go/protocol"
	"github.com/voocel/mcp-sdk-go/server"
)

//nolint:gocognit,revive // branchy by design; splitting would scatter one sequential algorithm
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
			return mcpToolError(fmt.Sprintf("jinn_route: create route id: %v", err))
		}
		route.RouteID = routeID
		return protocol.NewToolResultStructured(route)
	}
}

func mcpToolError(message string) (protocol.ToolResponse, error) {
	return protocol.NewToolResultError(message), nil
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
	return tool != "web_fetch" && tool != "web_search" && tool != "web_content" && tool != "source_check"
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
