package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
)

const (
	mcpExplorerFormatHuman      = "human"
	mcpExplorerFormatJSON       = "json"
	mcpExplorerFormatSignatures = "signatures"
)

// writeMCPExplorerOutput keeps machine output the default and makes the
// terminal-oriented form safe to paste into logs or display in a terminal.
func writeMCPExplorerOutput(writer io.Writer, format string, value any) error {
	if format == "" || format == mcpExplorerFormatJSON {
		return json.NewEncoder(writer).Encode(value)
	}
	if format != mcpExplorerFormatHuman {
		return fmt.Errorf("invalid --format %q: use json or human", format)
	}
	encoded, err := renderMCPExplorerHuman(value)
	if err != nil {
		return err
	}
	_, err = writer.Write(encoded)
	return err
}

func renderMCPExplorerHuman(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var sanitized any
	if decodeErr := decoder.Decode(&sanitized); decodeErr != nil {
		return nil, decodeErr
	}
	sanitized = sanitizeMCPExplorerHumanValue(sanitized)
	pretty, err := json.MarshalIndent(sanitized, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(pretty, '\n'), nil
}

func sanitizeMCPExplorerHumanValue(value any) any {
	switch item := value.(type) {
	case string:
		return sanitizeMCPExplorerHumanText(item)
	case []any:
		for index := range item {
			item[index] = sanitizeMCPExplorerHumanValue(item[index])
		}
	case map[string]any:
		sanitized := make(map[string]any, len(item))
		for key, child := range item {
			sanitized[sanitizeMCPExplorerHumanText(key)] = sanitizeMCPExplorerHumanValue(child)
		}
		return sanitized
	}
	return value
}

func sanitizeMCPExplorerHumanText(value string) string {
	var builder strings.Builder
	for _, character := range value {
		if character == '\\' {
			builder.WriteString(`\\`)
			continue
		}
		if unicode.IsControl(character) || unicode.In(character, unicode.Cf) {
			if character <= 0xffff {
				fmt.Fprintf(&builder, "\\u%04x", character)
			} else {
				fmt.Fprintf(&builder, "\\U%08x", character)
			}
			continue
		}
		builder.WriteRune(character)
	}
	return builder.String()
}

func collectMCPExplorerJSONOrHumanFormat(args []string) (string, []string, error) {
	format := ""
	remaining := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == mcpExplorerOptionFormat {
			if index+1 >= len(args) {
				return "", nil, errors.New("--format requires a value")
			}
			index++
			format = args[index]
			continue
		}
		if strings.HasPrefix(arg, mcpExplorerOptionFormat+"=") {
			format = strings.TrimPrefix(arg, mcpExplorerOptionFormat+"=")
			continue
		}
		remaining = append(remaining, arg)
	}
	if format != "" && format != mcpExplorerFormatJSON && format != mcpExplorerFormatHuman {
		return "", nil, fmt.Errorf("invalid --format %q: use json or human", format)
	}
	return format, remaining, nil
}
