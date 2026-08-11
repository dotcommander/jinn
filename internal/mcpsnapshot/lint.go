package mcpsnapshot

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/voocel/mcp-sdk-go/protocol"
)

const warningBidiControl = "bidi_control"

// Warning identifies advisory, unmodified untrusted server metadata.
type Warning struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// LintImplementation examines self-reported server identity without changing it.
func LintImplementation(server *protocol.Implementation, base string) []Warning {
	if server == nil {
		return nil
	}
	warnings := append(lintText(server.Name, base+"/name"), lintText(server.Title, base+"/title")...)
	warnings = append(warnings, lintText(server.Version, base+"/version")...)
	sortWarnings(warnings)
	return warnings
}

// LintTool examines copied metadata without changing it.
func LintTool(tool *protocol.Tool, base string) []Warning {
	if tool == nil {
		return nil
	}
	warnings := lintText(tool.Name, base+"/name")
	warnings = append(warnings, lintText(tool.Title, base+"/title")...)
	warnings = append(warnings, lintText(tool.Description, base+"/description")...)
	if len(tool.Description) > 8<<10 {
		warnings = append(warnings, Warning{Code: "description_too_large", Path: base + "/description", Message: "description exceeds 8 KiB"})
	}
	for _, schema := range []struct {
		path  string
		value any
	}{{"/inputSchema", tool.InputSchema}, {"/outputSchema", tool.OutputSchema}} {
		encoded, err := CanonicalJSON(schema.value)
		if err == nil && len(encoded) > 256<<10 {
			warnings = append(warnings, Warning{Code: "schema_too_large", Path: base + schema.path, Message: "schema exceeds 256 KiB"})
		}
		warnings = append(warnings, lintValue(schema.value, base+schema.path)...)
	}
	for index, icon := range tool.Icons {
		path := fmt.Sprintf("%s/icons/%d/src", base, index)
		if strings.HasPrefix(strings.ToLower(icon.Src), "data:") {
			warnings = append(warnings, Warning{Code: "data_icon_uri", Path: path, Message: "icon uses a data: URI"})
		}
		warnings = append(warnings, lintText(icon.Src, path)...)
		warnings = append(warnings, lintText(icon.MimeType, fmt.Sprintf("%s/icons/%d/mimeType", base, index))...)
		for sizeIndex, size := range icon.Sizes {
			warnings = append(warnings, lintText(size, fmt.Sprintf("%s/icons/%d/sizes/%d", base, index, sizeIndex))...)
		}
	}
	warnings = append(warnings, lintValue(tool.Meta, base+"/_meta")...)
	if tool.Annotations != nil {
		warnings = append(warnings, lintText(tool.Annotations.Title, base+"/annotations/title")...)
	}
	sortWarnings(warnings)
	return warnings
}

func lintValue(value any, path string) []Warning {
	canonical, err := CanonicalJSON(value)
	if err != nil {
		return nil
	}
	var decoded any
	if json.Unmarshal(canonical, &decoded) != nil {
		return nil
	}
	return lintDecodedValue(decoded, path)
}

func lintDecodedValue(value any, path string) []Warning {
	switch item := value.(type) {
	case string:
		return lintText(item, path)
	case map[string]any:
		keys := make([]string, 0, len(item))
		for key := range item {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		warnings := make([]Warning, 0)
		for _, key := range keys {
			child := item[key]
			warnings = append(warnings, lintText(key, path+"/"+jsonPointerToken(key))...)
			warnings = append(warnings, lintDecodedValue(child, path+"/"+jsonPointerToken(key))...)
		}
		return warnings
	case []any:
		warnings := make([]Warning, 0)
		for index, child := range item {
			warnings = append(warnings, lintDecodedValue(child, fmt.Sprintf("%s/%d", path, index))...)
		}
		return warnings
	default:
		return nil
	}
}

//nolint:gocyclo // Each independent metadata hazard emits its own advisory code.
func lintText(value, path string) []Warning {
	warnings := make([]Warning, 0, 3)
	for _, runeValue := range value {
		if runeValue < 0x20 && runeValue != '\t' && runeValue != '\n' && runeValue != '\r' {
			warnings = append(warnings, Warning{Code: "ascii_control", Path: path, Message: "metadata contains an ASCII control character"})
			break
		}
	}
	if strings.Contains(value, "<!--") || strings.Contains(value, "-->") {
		warnings = append(warnings, Warning{Code: "html_comment", Path: path, Message: "metadata contains an HTML comment"})
	}
	for _, runeValue := range value {
		if runeValue == '\ufeff' || runeValue == '\u200b' || runeValue == '\u200c' || runeValue == '\u200d' || runeValue == '\u2060' || runeValue == '\u180e' {
			warnings = append(warnings, Warning{Code: "zero_width_or_bom", Path: path, Message: "metadata contains a zero-width character or BOM"})
			break
		}
		if unicode.Is(unicode.Bidi_Control, runeValue) {
			warnings = append(warnings, Warning{Code: warningBidiControl, Path: path, Message: "metadata contains a bidi control character"})
			break
		}
	}
	return warnings
}
