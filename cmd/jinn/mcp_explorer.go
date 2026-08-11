package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/dotcommander/jinn/internal/mcpexplore"
	"github.com/dotcommander/jinn/internal/toolschema"
	"github.com/voocel/mcp-sdk-go/protocol"
)

const mcpExplorerHelp = `Usage:
  jinn mcp list ENDPOINT [--format=json|human|signatures] [--timeout 30s]
  jinn mcp inspect ENDPOINT TOOL [--format=json|human] [--timeout 30s]
  jinn mcp call ENDPOINT TOOL [--format=json|human] [--args JSON] [-a NAME VALUE]... [--select RFC6901_POINTER] [--head N] [--max-bytes N] [--timeout 30s]
  jinn mcp cost ENDPOINT [--format=json|human] [--encoding=o200k_base|cl100k_base] [--timeout 30s]
  jinn mcp export ENDPOINT [--format=mcp|openai-responses] [--timeout 30s]
  jinn mcp servers list [--format=json|human|names]
  jinn mcp servers add NAME --http URL [--token-env ENV] [--replace]
  jinn mcp servers add NAME --stdio PATH [--arg ARG]... [--pass-env NAME]... [--replace]
  jinn mcp servers remove NAME
  jinn mcp doctor [@NAME|--all] [--format=json|human] [--timeout 30s]
  jinn mcp snapshot @NAME --accept
  jinn mcp schema-diff @NAME [--format=json|human]
  jinn mcp batch @NAME [--format=json|human] [--file FILE|-] [--timeout 2m] [--call-timeout 30s] [--dry-run]

  jinn mcp list --command PATH [--arg ARG]... [--format=json|human|signatures] [--timeout 30s]
  jinn mcp inspect --command PATH [--arg ARG]... TOOL [--format=json|human] [--timeout 30s]
  jinn mcp call --command PATH [--arg ARG]... TOOL [--format=json|human] [--args JSON] [-a NAME VALUE]... [--select RFC6901_POINTER] [--head N] [--max-bytes N] [--timeout 30s]
  jinn mcp cost --command PATH [--arg ARG]... [--format=json|human] [--encoding=o200k_base|cl100k_base] [--timeout 30s]
  jinn mcp export --command PATH [--arg ARG]... [--format=mcp|openai-responses] [--timeout 30s]

ENDPOINT must be an http:// or https:// MCP endpoint, or a registered @alias.
--command starts an MCP subprocess directly with explicit argv (no shell).
HTTP bearer tokens come only
from JINN_MCP_HTTP_TOKEN and are never printed. Tool errors are returned as JSON
with isError=true; connection, protocol, and argument errors exit nonzero.
`

type mcpExplorerHelpError struct{}

func (mcpExplorerHelpError) Error() string { return mcpExplorerHelp }

type mcpExplorerListOutput struct {
	Server    *protocol.Implementation `json:"server"`
	Discovery *protocol.DiscoverResult `json:"discovery"`
	Tools     []*protocol.Tool         `json:"tools"`
}

type mcpExplorerSignatureTool struct {
	Name        string `json:"-"`
	Signature   string `json:"signature"`
	Description string `json:"description"`
}

type mcpExplorerSignaturesOutput struct {
	Server *protocol.Implementation   `json:"server"`
	Tools  []mcpExplorerSignatureTool `json:"tools"`
}

type mcpExplorerCallOutput struct {
	ResultType        string               `json:"resultType"`
	Content           protocol.ContentList `json:"content"`
	StructuredContent any                  `json:"structuredContent"`
	IsError           bool                 `json:"isError"`
}

//nolint:gocyclo // Top-level action dispatch keeps one client lifecycle for explorer operations.
func runMCPExplorer(ctx context.Context, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "servers":
			return runMCPServers(args[1:])
		case "doctor":
			return runMCPDoctor(ctx, args[1:])
		case "snapshot":
			return runMCPSnapshot(ctx, args[1:])
		case "schema-diff":
			return runMCPSchemaDiff(ctx, args[1:])
		case "batch":
			return runMCPBatch(ctx, args[1:])
		case mcpExplorerActionExport:
			return runMCPExport(ctx, args[1:])
		}
	}
	config, err := parseMCPExplorer(args)
	if err != nil {
		return mcpExplorerProjectionCLIError(err)
	}
	ctx, cancel := context.WithTimeout(ctx, config.timeout)
	defer cancel()
	c, err := mcpexplore.New(ctx, config.target)
	if err != nil {
		return fmt.Errorf("connect MCP server: %w", err)
	}
	defer func() { _ = c.Close() }()

	switch config.action {
	case mcpExplorerActionList:
		discovery, tools, err := discoverMCPExplorerTools(ctx, c)
		if err != nil {
			return err
		}
		if config.format == mcpExplorerFormatSignatures {
			return json.NewEncoder(os.Stdout).Encode(newMCPExplorerSignaturesOutput(discovery.Meta.ServerInfo, tools))
		}
		return writeMCPExplorerOutput(os.Stdout, config.format, mcpExplorerListOutput{Server: discovery.Meta.ServerInfo, Discovery: discovery, Tools: tools})
	case mcpExplorerActionInspect:
		tool, err := c.Inspect(ctx, config.tool)
		if err != nil {
			if strings.HasPrefix(err.Error(), "MCP tool ") {
				return err
			}
			return fmt.Errorf("MCP tools/list: %w", err)
		}
		return writeMCPExplorerOutput(os.Stdout, config.format, tool)
	case mcpExplorerActionCall:
		result, err := c.Call(ctx, config.tool, config.arguments)
		if err != nil {
			return fmt.Errorf("MCP tools/call %q: %w", config.tool, err)
		}
		return mcpExplorerProjectionCLIError(writeMCPExplorerCallFormattedOutput(os.Stdout, config.format, mcpExplorerCallOutput{ResultType: protocol.ResultTypeComplete, Content: result.Content, StructuredContent: result.StructuredContent, IsError: result.IsError}, config.projection))
	case mcpExplorerActionCost:
		return runMCPExplorerCost(ctx, c, config.encoding, config.format)
	default:
		return errors.New("MCP explorer: unsupported action")
	}
}

func mcpExplorerProjectionCLIError(err error) error {
	var projectionErr *mcpExplorerProjectionError
	if !errors.As(err, &projectionErr) {
		return err
	}
	return fail(jinn.Response{Error: projectionErr.message, ErrorCode: jinn.ErrCodeInvalidArgs, Suggestion: projectionErr.suggestion})
}

func writeMCPExplorerCallOutput(writer io.Writer, output mcpExplorerCallOutput, projection mcpExplorerProjectionOptions) error {
	if output.IsError || (!projection.hasSelect && !projection.hasMaxBytes) {
		return json.NewEncoder(writer).Encode(output)
	}
	return renderMCPExplorerProjectedCallOutput(writer, output, projection)
}

func writeMCPExplorerCallFormattedOutput(writer io.Writer, format string, output mcpExplorerCallOutput, projection mcpExplorerProjectionOptions) error {
	if format == "" || format == mcpExplorerFormatJSON {
		return writeMCPExplorerCallOutput(writer, output, projection)
	}
	if output.IsError {
		return writeMCPExplorerOutput(writer, format, output)
	}
	value, err := projectMCPExplorerCallValue(output, projection)
	if err != nil {
		return err
	}
	if format != mcpExplorerFormatHuman {
		return writeMCPExplorerOutput(writer, format, value)
	}
	rendered, err := renderMCPExplorerHuman(value)
	if err != nil {
		return err
	}
	if projection.hasMaxBytes && len(rendered) > projection.maxBytes {
		return newMCPExplorerProjectionError(fmt.Sprintf("MCP call output is %d bytes, exceeding --max-bytes %d", len(rendered), projection.maxBytes), "increase --max-bytes or use --select and --head to reduce the output")
	}
	_, err = writer.Write(rendered)
	return err
}

func discoverMCPExplorerTools(ctx context.Context, c *mcpexplore.Client) (*protocol.DiscoverResult, []*protocol.Tool, error) {
	discovery, err := c.Discover(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("MCP discovery: %w", err)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("MCP tools/list: %w", err)
	}
	return discovery, tools, nil
}

func newMCPExplorerSignaturesOutput(server *protocol.Implementation, tools []*protocol.Tool) mcpExplorerSignaturesOutput {
	items := make([]mcpExplorerSignatureTool, 0, len(tools))
	for _, tool := range tools {
		items = append(items, mcpExplorerSignatureTool{Name: tool.Name, Signature: toolschema.Render(tool.Name, tool.InputSchema), Description: tool.Description})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return mcpExplorerSignaturesOutput{Server: server, Tools: items}
}
