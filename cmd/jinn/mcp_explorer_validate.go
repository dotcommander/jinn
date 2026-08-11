package main

import (
	"errors"
	"fmt"
)

func validateMCPExplorerActionOptions(state mcpExplorerParseState) error {
	config := state.config
	if state.hasCommandArg && config.target.Command == "" {
		return errors.New("--arg requires --command")
	}
	if state.hasCallArguments && config.action != mcpExplorerActionCall {
		return errors.New("--args and --argument are only valid for mcp call")
	}
	if err := validateMCPExplorerFormatCompatibility(config.action, config.format); err != nil {
		return err
	}
	if config.encoding != "" && config.action != mcpExplorerActionCost {
		return errors.New("--encoding is only valid for mcp cost")
	}
	return validateMCPExplorerProjectionOptions(config)
}

func validateMCPExplorerFormatCompatibility(action, format string) error {
	if format == "" {
		return nil
	}
	switch action {
	case mcpExplorerActionList:
		if format != mcpExplorerFormatJSON && format != mcpExplorerFormatHuman && format != mcpExplorerFormatSignatures {
			return errors.New("invalid --format for mcp list: use json, human, or signatures")
		}
	case mcpExplorerActionExport:
		if format != mcpExplorerExportFormatMCP && format != mcpExplorerExportFormatOpenAIResponses {
			return errors.New("invalid --format for mcp export: use mcp or openai-responses")
		}
	default:
		if format == mcpExplorerFormatSignatures {
			return errors.New("--format=signatures is only valid for mcp list")
		}
		if format != mcpExplorerFormatJSON && format != mcpExplorerFormatHuman {
			return fmt.Errorf("invalid --format for mcp %s: use json or human", action)
		}
	}
	return nil
}
