package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dotcommander/jinn/internal/mcpexplore"
)

type mcpBatchOptionToken struct {
	name        string
	inlineValue string
	hasInline   bool
	isOption    bool
}

func parseMCPBatch(args []string) (mcpBatchConfig, error) {
	config := mcpBatchConfig{file: "-", timeout: 2 * time.Minute, callTimeout: 30 * time.Second}
	operands := make([]string, 0, 1)
	for index := 0; index < len(args); index++ {
		token := decodeMCPBatchOptionToken(args[index])
		if !token.isOption {
			operands = append(operands, token.name)
			continue
		}
		if token.name == mcpBatchOptionDryRun {
			if err := applyMCPBatchOption(&config, token, ""); err != nil {
				return config, err
			}
			continue
		}
		if !isMCPBatchValueOption(token.name) {
			return config, fmt.Errorf("unknown mcp batch option %q", token.name)
		}
		value, next, err := collectMCPBatchOptionOperand(args, index, token)
		if err != nil {
			return config, err
		}
		index = next
		if err := applyMCPBatchOption(&config, token, value); err != nil {
			return config, err
		}
	}
	return validateMCPBatchAlias(config, operands)
}

func decodeMCPBatchOptionToken(arg string) mcpBatchOptionToken {
	if !strings.HasPrefix(arg, "--") {
		return mcpBatchOptionToken{name: arg}
	}
	name, value, hasInline := strings.Cut(arg, "=")
	return mcpBatchOptionToken{name: name, inlineValue: value, hasInline: hasInline, isOption: true}
}

func isMCPBatchValueOption(name string) bool {
	switch name {
	case mcpBatchOptionFile, mcpExplorerOptionTimeout, mcpBatchOptionCallTimeout, mcpExplorerOptionFormat:
		return true
	default:
		return false
	}
}

func collectMCPBatchOptionOperand(args []string, index int, token mcpBatchOptionToken) (string, int, error) {
	if token.hasInline {
		return token.inlineValue, index, nil
	}
	remaining := args[index+1:]
	if len(remaining) == 0 {
		return "", index, fmt.Errorf("%s requires a value", token.name)
	}
	return remaining[0], index + 1, nil
}

func applyMCPBatchOption(config *mcpBatchConfig, token mcpBatchOptionToken, value string) error {
	switch token.name {
	case mcpBatchOptionFile:
		config.file = value
	case mcpExplorerOptionTimeout, mcpBatchOptionCallTimeout:
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return fmt.Errorf("invalid %s %q", token.name, value)
		}
		if token.name == mcpExplorerOptionTimeout {
			config.timeout = duration
		} else {
			config.callTimeout = duration
		}
	case mcpExplorerOptionFormat:
		if value != mcpExplorerFormatJSON && value != mcpExplorerFormatHuman {
			return fmt.Errorf("invalid --format %q: use json or human", value)
		}
		config.format = value
	case mcpBatchOptionDryRun:
		if token.hasInline {
			return errors.New("--dry-run does not accept a value")
		}
		config.dryRun = true
	}
	return nil
}

func validateMCPBatchAlias(config mcpBatchConfig, operands []string) (mcpBatchConfig, error) {
	if len(operands) != 1 || !strings.HasPrefix(operands[0], "@") || len(operands[0]) == 1 {
		return config, errors.New("usage: jinn mcp batch @NAME [--file FILE|-] [--timeout 2m] [--call-timeout 30s] [--dry-run]")
	}
	config.alias = strings.TrimPrefix(operands[0], "@")
	if err := mcpexplore.ValidateAlias(config.alias); err != nil {
		return config, err
	}
	return config, nil
}
