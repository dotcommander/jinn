package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	mcpExplorerOptionSelect   = "--select"
	mcpExplorerOptionHead     = "--head"
	mcpExplorerOptionMaxBytes = "--max-bytes"
)

type mcpExplorerProjectionOptions struct {
	pointer     string
	head        int
	maxBytes    int
	hasSelect   bool
	hasHead     bool
	hasMaxBytes bool
}

type mcpExplorerProjectionOutput struct {
	SchemaVersion int                          `json:"schema_version"`
	Value         any                          `json:"value"`
	Projection    mcpExplorerProjectionDetails `json:"projection"`
}

type mcpExplorerProjectionDetails struct {
	Pointer       string `json:"pointer"`
	TotalItems    *int   `json:"total_items,omitempty"`
	ReturnedItems *int   `json:"returned_items,omitempty"`
	Truncated     bool   `json:"truncated"`
}

type mcpExplorerProjectionError struct {
	message    string
	suggestion string
}

func (e *mcpExplorerProjectionError) Error() string { return e.message }

func newMCPExplorerProjectionError(message, suggestion string) error {
	return &mcpExplorerProjectionError{message: message, suggestion: suggestion}
}

func collectMCPExplorerProjectionOption(args []string, index int, state *mcpExplorerParseState) (int, bool, error) {
	name := args[index]
	switch name {
	case mcpExplorerOptionSelect:
		value, next, err := readMCPExplorerOptionOperand(args, index, name)
		if err != nil {
			return index, true, err
		}
		state.config.projection.pointer = value
		state.config.projection.hasSelect = true
		return next, true, nil
	case mcpExplorerOptionHead, mcpExplorerOptionMaxBytes:
		value, next, err := readMCPExplorerOptionOperand(args, index, name)
		if err != nil {
			return index, true, err
		}
		parsed, err := parseMCPExplorerNonNegativeOption(name, value)
		if err != nil {
			return index, true, err
		}
		assignMCPExplorerProjectionLimit(&state.config.projection, name, parsed)
		return next, true, nil
	default:
		return index, false, nil
	}
}

func collectMCPExplorerInlineProjectionOption(arg string, state *mcpExplorerParseState) (bool, error) {
	if strings.HasPrefix(arg, mcpExplorerOptionSelect+"=") {
		state.config.projection.pointer = strings.TrimPrefix(arg, mcpExplorerOptionSelect+"=")
		state.config.projection.hasSelect = true
		return true, nil
	}
	for _, name := range []string{mcpExplorerOptionHead, mcpExplorerOptionMaxBytes} {
		if strings.HasPrefix(arg, name+"=") {
			value := strings.TrimPrefix(arg, name+"=")
			parsed, err := parseMCPExplorerNonNegativeOption(name, value)
			if err != nil {
				return true, err
			}
			assignMCPExplorerProjectionLimit(&state.config.projection, name, parsed)
			return true, nil
		}
	}
	return false, nil
}

func assignMCPExplorerProjectionLimit(options *mcpExplorerProjectionOptions, name string, value int) {
	if name == mcpExplorerOptionHead {
		options.head = value
		options.hasHead = true
		return
	}
	options.maxBytes = value
	options.hasMaxBytes = true
}

func parseMCPExplorerNonNegativeOption(name, value string) (int, error) {
	parsed, err := strconv.ParseInt(value, 10, 0)
	if err != nil || parsed < 0 {
		return 0, newMCPExplorerProjectionError(fmt.Sprintf("invalid %s %q: must be a non-negative integer", name, value), "use a whole number greater than or equal to zero")
	}
	return int(parsed), nil
}

func validateMCPExplorerProjectionOptions(config mcpExplorerConfig) error {
	options := config.projection
	if (options.hasSelect || options.hasHead || options.hasMaxBytes) && config.action != mcpExplorerActionCall {
		return newMCPExplorerProjectionError("--select, --head, and --max-bytes are only valid for mcp call", "use these options with jinn mcp call")
	}
	if options.hasHead && !options.hasSelect {
		return newMCPExplorerProjectionError("--head requires --select", "add --select RFC6901_POINTER before --head")
	}
	return nil
}

func renderMCPExplorerProjectedCallOutput(writer io.Writer, output mcpExplorerCallOutput, options mcpExplorerProjectionOptions) error {
	value, err := projectMCPExplorerCallValue(output, options)
	if err != nil {
		return err
	}
	encoded, err := renderMCPExplorerJSON(value)
	if err != nil {
		return err
	}
	if options.hasMaxBytes && len(encoded) > options.maxBytes {
		return newMCPExplorerProjectionError(fmt.Sprintf("MCP call output is %d bytes, exceeding --max-bytes %d", len(encoded), options.maxBytes), "increase --max-bytes or use --select and --head to reduce the output")
	}
	_, err = writer.Write(encoded)
	return err
}

// projectMCPExplorerCallValue shares P3's lossless projection semantics with batch.
//
//nolint:nestif // Selection and array-head metadata are one lossless projection operation.
func projectMCPExplorerCallValue(output mcpExplorerCallOutput, options mcpExplorerProjectionOptions) (any, error) {
	value := any(output)
	if options.hasSelect {
		selected, err := selectMCPExplorerCallOutput(output, options.pointer)
		if err != nil {
			return nil, err
		}
		projection := mcpExplorerProjectionDetails{Pointer: options.pointer}
		if items, ok := selected.([]any); ok {
			total := len(items)
			returned := total
			if options.hasHead && returned > options.head {
				returned = options.head
			}
			projection.TotalItems = &total
			projection.ReturnedItems = &returned
			projection.Truncated = returned < total
			value = items[:returned]
		} else if options.hasHead {
			return nil, newMCPExplorerProjectionError("--head requires --select to resolve to an array", "select an array value or omit --head")
		} else {
			value = selected
		}
		value = mcpExplorerProjectionOutput{SchemaVersion: 1, Value: value, Projection: projection}
	}
	return value, nil
}

func selectMCPExplorerCallOutput(output mcpExplorerCallOutput, pointer string) (any, error) {
	encoded, err := json.Marshal(output)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return resolveMCPExplorerJSONPointer(value, pointer)
}

func resolveMCPExplorerJSONPointer(value any, pointer string) (any, error) {
	if pointer == "" {
		return value, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, newMCPExplorerProjectionError(fmt.Sprintf("invalid --select pointer %q: must be empty or start with '/'", pointer), "use an RFC 6901 JSON Pointer")
	}
	current := value
	for _, rawToken := range strings.Split(pointer[1:], "/") {
		token, err := decodeMCPExplorerPointerToken(pointer, rawToken)
		if err != nil {
			return nil, err
		}
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[token]
			if !ok {
				return nil, newMCPExplorerProjectionError(fmt.Sprintf("--select pointer %q: path not found at %q", pointer, token), "check the selected object key and pointer escaping")
			}
			current = next
		case []any:
			index, err := parseMCPExplorerArrayIndex(pointer, token)
			if err != nil {
				return nil, err
			}
			if index >= len(node) {
				return nil, newMCPExplorerProjectionError(fmt.Sprintf("--select pointer %q: array index %q is out of range", pointer, token), "choose an existing zero-based array index")
			}
			current = node[index]
		default:
			return nil, newMCPExplorerProjectionError(fmt.Sprintf("--select pointer %q: path not found at %q", pointer, token), "select only through object keys and array indexes")
		}
	}
	return current, nil
}

func decodeMCPExplorerPointerToken(pointer, token string) (string, error) {
	var decoded strings.Builder
	for i := 0; i < len(token); i++ {
		if token[i] != '~' {
			decoded.WriteByte(token[i])
			continue
		}
		if i+1 == len(token) || (token[i+1] != '0' && token[i+1] != '1') {
			return "", newMCPExplorerProjectionError(fmt.Sprintf("invalid --select pointer %q: invalid escape in %q", pointer, token), "use ~0 for '~' and ~1 for '/'")
		}
		if token[i+1] == '0' {
			decoded.WriteByte('~')
		} else {
			decoded.WriteByte('/')
		}
		i++
	}
	return decoded.String(), nil
}

func parseMCPExplorerArrayIndex(pointer, token string) (int, error) {
	if token == "" || (len(token) > 1 && token[0] == '0') {
		return 0, newMCPExplorerProjectionError(fmt.Sprintf("--select pointer %q: invalid array index %q", pointer, token), "use a zero-based array index without leading zeroes")
	}
	for _, character := range token {
		if character < '0' || character > '9' {
			return 0, newMCPExplorerProjectionError(fmt.Sprintf("--select pointer %q: invalid array index %q", pointer, token), "use a zero-based array index without leading zeroes")
		}
	}
	index, err := strconv.ParseInt(token, 10, 0)
	if err != nil {
		return 0, newMCPExplorerProjectionError(fmt.Sprintf("--select pointer %q: invalid array index %q", pointer, token), "use a zero-based array index without leading zeroes")
	}
	return int(index), nil
}
