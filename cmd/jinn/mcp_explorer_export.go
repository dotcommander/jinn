package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"

	"github.com/dotcommander/jinn/internal/mcpexplore"
	"github.com/voocel/mcp-sdk-go/protocol"
)

const (
	mcpExplorerExportFormatMCP             = "mcp"
	mcpExplorerExportFormatOpenAIResponses = "openai-responses"
)

var mcpExportToolName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type mcpExportMCPOutput struct {
	Tools []*protocol.Tool `json:"tools"`
}

type mcpExportResponsesFunction struct {
	Type        string              `json:"type"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Parameters  protocol.JSONSchema `json:"parameters"`
	Strict      bool                `json:"strict"`
}

func runMCPExport(ctx context.Context, args []string) error {
	config, err := parseMCPExplorer(append([]string{mcpExplorerActionExport}, args...))
	if err != nil {
		return mcpExplorerProjectionCLIError(err)
	}
	ctx, cancel := context.WithTimeout(ctx, config.timeout)
	defer cancel()
	client, err := mcpexplore.New(ctx, config.target)
	if err != nil {
		return fmt.Errorf("connect MCP server: %w", err)
	}
	defer func() { _ = client.Close() }()
	tools, err := client.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("MCP tools/list: %w", err)
	}
	output, err := newMCPExportOutput(config.format, tools)
	if err != nil {
		return err
	}
	return writeMCPExplorerOutput(os.Stdout, mcpExplorerFormatJSON, output)
}

func newMCPExportOutput(format string, tools []*protocol.Tool) (any, error) {
	sorted := append([]*protocol.Tool(nil), tools...)
	for _, tool := range sorted {
		if tool == nil {
			return nil, errors.New("MCP tools/list returned a null tool")
		}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	switch format {
	case "", mcpExplorerExportFormatMCP:
		return mcpExportMCPOutput{Tools: sorted}, nil
	case mcpExplorerExportFormatOpenAIResponses:
		output := make([]mcpExportResponsesFunction, 0, len(sorted))
		for _, tool := range sorted {
			if !mcpExportToolName.MatchString(tool.Name) {
				return nil, fmt.Errorf("MCP tool name %q cannot be exported to openai-responses: must match [A-Za-z0-9_-]{1,64}", tool.Name)
			}
			output = append(output, mcpExportResponsesFunction{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema, Strict: false})
		}
		return output, nil
	default:
		return nil, fmt.Errorf("invalid --format %q: use mcp or openai-responses", format)
	}
}
