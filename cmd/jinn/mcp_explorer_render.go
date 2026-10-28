package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf16"
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
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return []byte(sanitizeMCPExplorerEncodedJSON(string(pretty)) + "\n"), nil
}

// JSON already escapes control characters and backslashes. Only format-control
// runes remain raw; escape them at this final terminal presentation boundary.
func sanitizeMCPExplorerEncodedJSON(value string) string {
	var builder strings.Builder
	for _, character := range value {
		if unicode.In(character, unicode.Cf) || (unicode.IsControl(character) && character > 0x7f) {
			if character <= 0xffff {
				fmt.Fprintf(&builder, "\\u%04x", character)
			} else {
				high, low := utf16.EncodeRune(character)
				fmt.Fprintf(&builder, "\\u%04x\\u%04x", high, low)
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
