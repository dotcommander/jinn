package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/dotcommander/jinn/internal/mcpexplore"
)

const (
	mcpExplorerActionCall       = "call"
	mcpExplorerActionCost       = "cost"
	mcpExplorerActionExport     = "export"
	mcpExplorerActionInspect    = "inspect"
	mcpExplorerActionList       = "list"
	mcpExplorerDefaultTimeout   = 30 * time.Second
	mcpExplorerHelpLong         = "--help"
	mcpExplorerHelpShort        = "-h"
	mcpExplorerHelpWord         = "help"
	mcpExplorerOptionArg        = "--arg"
	mcpExplorerOptionArgShort   = "-a"
	mcpExplorerOptionArgs       = "--args"
	mcpExplorerOptionCommand    = "--command"
	mcpExplorerOptionEncoding   = "--encoding"
	mcpExplorerOptionFormat     = "--format"
	mcpExplorerOptionTimeout    = "--timeout"
	mcpExplorerOptionToken      = "--token"
	mcpExplorerTokenOptionError = "--token is unsupported; set JINN_MCP_HTTP_TOKEN instead" //nolint:gosec // Names the environment variable, not a credential.
)

type mcpExplorerConfig struct {
	action     string
	target     mcpexplore.Target
	tool       string
	timeout    time.Duration
	arguments  map[string]any
	format     string
	encoding   string
	projection mcpExplorerProjectionOptions
}

type mcpExplorerParseState struct {
	config           mcpExplorerConfig
	positionals      []string
	hasCommandArg    bool
	hasCallArguments bool
}

func parseMCPExplorer(args []string) (mcpExplorerConfig, error) {
	if len(args) == 0 || args[0] == mcpExplorerHelpLong || args[0] == mcpExplorerHelpShort || args[0] == mcpExplorerHelpWord {
		return mcpExplorerConfig{}, mcpExplorerHelpError{}
	}
	state := mcpExplorerParseState{
		config:      mcpExplorerConfig{action: args[0], timeout: mcpExplorerDefaultTimeout, arguments: make(map[string]any)},
		positionals: make([]string, 0, 2),
	}
	switch state.config.action {
	case mcpExplorerActionList, mcpExplorerActionInspect, mcpExplorerActionCall, mcpExplorerActionCost, mcpExplorerActionExport:
	default:
		return state.config, fmt.Errorf("unknown mcp command %q: use list, inspect, call, cost, or export", state.config.action)
	}
	if err := collectMCPExplorerOptions(args[1:], &state); err != nil {
		return state.config, err
	}
	return finalizeMCPExplorerOptions(state)
}

func collectMCPExplorerOptions(args []string, state *mcpExplorerParseState) error {
	for i := 0; i < len(args); i++ {
		next, handled, err := collectMCPExplorerExactOption(args, i, state)
		if err != nil {
			return err
		}
		if handled {
			i = next
			continue
		}
		if err := collectMCPExplorerInlineOptionOrPositional(args[i], state); err != nil {
			return err
		}
	}
	return nil
}

func collectMCPExplorerExactOption(args []string, index int, state *mcpExplorerParseState) (int, bool, error) {
	if next, handled, err := collectMCPExplorerTransportOption(args, index, state); handled || err != nil {
		return next, handled, err
	}
	return collectMCPExplorerActionOption(args, index, state)
}

func collectMCPExplorerTransportOption(args []string, index int, state *mcpExplorerParseState) (int, bool, error) {
	arg := args[index]
	switch arg {
	case mcpExplorerOptionToken:
		return index, true, errors.New(mcpExplorerTokenOptionError)
	case mcpExplorerOptionCommand:
		value, next, err := readMCPExplorerOptionOperand(args, index, arg)
		if err != nil {
			return index, true, err
		}
		state.config.target.Command = value
		return next, true, nil
	case mcpExplorerOptionArg:
		state.hasCommandArg = true
		value, next, err := readMCPExplorerOptionOperand(args, index, arg)
		if err != nil {
			return index, true, err
		}
		state.config.target.CommandArgs = append(state.config.target.CommandArgs, value)
		return next, true, nil
	case mcpExplorerOptionTimeout:
		value, next, err := readMCPExplorerOptionOperand(args, index, arg)
		if err != nil {
			return index, true, err
		}
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return index, true, fmt.Errorf("invalid --timeout %q", value)
		}
		state.config.timeout = duration
		return next, true, nil
	}
	return index, false, nil
}

func collectMCPExplorerActionOption(args []string, index int, state *mcpExplorerParseState) (int, bool, error) {
	arg := args[index]
	if next, handled, err := collectMCPExplorerProjectionOption(args, index, state); handled || err != nil {
		return next, handled, err
	}
	switch arg {
	case mcpExplorerOptionArgs:
		state.hasCallArguments = true
		value, next, err := readMCPExplorerOptionOperand(args, index, arg)
		if err != nil {
			return index, true, err
		}
		if err := mergeMCPExplorerJSONArgs(state.config.arguments, value); err != nil {
			return index, true, err
		}
		return next, true, nil
	case mcpExplorerOptionArgShort, "--argument":
		state.hasCallArguments = true
		name, next, err := readMCPExplorerOptionOperand(args, index, arg)
		if err != nil {
			return index, true, err
		}
		value, next, err := readMCPExplorerOptionOperand(args, next, arg)
		if err != nil {
			return index, true, err
		}
		state.config.arguments[name] = decodeMCPExplorerArgument(value)
		return next, true, nil
	case mcpExplorerOptionFormat:
		value, next, err := readMCPExplorerOptionOperand(args, index, arg)
		if err != nil {
			return index, true, err
		}
		state.config.format = value
		return next, true, nil
	case mcpExplorerOptionEncoding:
		value, next, err := readMCPExplorerOptionOperand(args, index, arg)
		if err != nil {
			return index, true, err
		}
		state.config.encoding = value
		return next, true, nil
	}
	return index, false, nil
}

func collectMCPExplorerInlineOptionOrPositional(arg string, state *mcpExplorerParseState) error {
	if handled, err := collectMCPExplorerInlineProjectionOption(arg, state); handled || err != nil {
		return err
	}
	if strings.HasPrefix(arg, mcpExplorerOptionToken+"=") {
		return errors.New(mcpExplorerTokenOptionError)
	}
	if strings.HasPrefix(arg, mcpExplorerOptionTimeout+"=") {
		value := strings.TrimPrefix(arg, mcpExplorerOptionTimeout+"=")
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return fmt.Errorf("invalid --timeout %q", value)
		}
		state.config.timeout = duration
		return nil
	}
	if strings.HasPrefix(arg, mcpExplorerOptionFormat+"=") {
		state.config.format = strings.TrimPrefix(arg, mcpExplorerOptionFormat+"=")
		return nil
	}
	if strings.HasPrefix(arg, mcpExplorerOptionEncoding+"=") {
		state.config.encoding = strings.TrimPrefix(arg, mcpExplorerOptionEncoding+"=")
		return nil
	}
	if strings.HasPrefix(arg, "--") {
		return fmt.Errorf("unknown mcp option %q", arg)
	}
	state.positionals = append(state.positionals, arg)
	return nil
}

func readMCPExplorerOptionOperand(args []string, index int, name string) (string, int, error) {
	next := index + 1
	if next >= len(args) {
		return "", index, fmt.Errorf("%s requires a value", name)
	}
	return args[next], next, nil
}

func finalizeMCPExplorerOptions(state mcpExplorerParseState) (mcpExplorerConfig, error) {
	config := state.config
	if err := validateMCPExplorerActionOptions(state); err != nil {
		return config, err
	}
	config, positionals, err := finalizeMCPExplorerTarget(config, state.positionals)
	if err != nil {
		return config, err
	}
	return finalizeMCPExplorerTool(config, positionals)
}

//nolint:nestif // Target selection preserves the existing endpoint/command error precedence.
func finalizeMCPExplorerTarget(config mcpExplorerConfig, positionals []string) (mcpExplorerConfig, []string, error) {
	if config.target.Command != "" && len(positionals) > 0 && (strings.HasPrefix(positionals[0], "http") || strings.HasPrefix(positionals[0], "@")) {
		return config, positionals, errors.New("use either ENDPOINT or --command, not both")
	}
	if config.target.Command == "" {
		if len(positionals) == 0 {
			return config, positionals, errors.New("MCP endpoint is required")
		}
		target, remaining := positionals[0], positionals[1:]
		if strings.HasPrefix(target, "@") {
			resolved, _, err := mcpexplore.AliasTarget(strings.TrimPrefix(target, "@"), os.Environ())
			if err != nil {
				return config, positionals, err
			}
			config.target, positionals = resolved, remaining
		} else {
			config.target.Endpoint, positionals = target, remaining
			if err := config.target.Validate(); err != nil {
				return config, positionals, err
			}
		}
	}
	return config, positionals, nil
}

func finalizeMCPExplorerTool(config mcpExplorerConfig, positionals []string) (mcpExplorerConfig, error) {
	if config.action == mcpExplorerActionInspect || config.action == mcpExplorerActionCall {
		if len(positionals) != 1 {
			return config, fmt.Errorf("mcp %s requires exactly one TOOL", config.action)
		}
		config.tool = positionals[0]
	} else if len(positionals) != 0 {
		return config, fmt.Errorf("mcp %s does not accept a tool name", config.action)
	}
	return config, nil
}

func decodeMCPExplorerArgument(value string) any {
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	if decoder.Decode(&decoded) == nil && decoder.Decode(&struct{}{}) == io.EOF {
		return decoded
	}
	return value
}

func mergeMCPExplorerJSONArgs(arguments map[string]any, raw string) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil || values == nil {
		return errors.New("--args must be a JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("--args must contain exactly one JSON object")
	}
	for key, value := range values {
		arguments[key] = value
	}
	return nil
}
