package webfetch

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Field extraction reasons: a field is either grounded on a source line
// ("found"), absent from the page ("not_on_page"), or present but not
// parseable as the requested type ("uncertain"). Extraction never dumps
// the whole page — it returns the grounded value plus the line it came
// from, or an explicit absence.
const (
	FieldFound     = "found"
	FieldNotOnPage = "not_on_page"
	FieldUncertain = "uncertain"
)

// FieldResult is one grounded extraction outcome.
type FieldResult struct {
	Field  string `json:"field"`
	Type   string `json:"type"`
	Status string `json:"status"`
	// Value is the typed value when Status is "found".
	Value any `json:"value,omitempty"`
	// Line is the 1-based source line the value was found on, when found.
	Line int `json:"line,omitempty"`
	// Source is the exact source line text, when a line was located.
	Source string `json:"source,omitempty"`
}

var numberToken = regexp.MustCompile(`[-+]?\d[\d,]*(?:\.\d+)?`)

// ExtractFields grounds each named field in content line by line. Field
// types are "string", "number", or "boolean"; anything else is rejected
// by the caller before extraction.
func ExtractFields(content string, fields map[string]string) []FieldResult {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := strings.Split(content, "\n")
	results := make([]FieldResult, 0, len(names))
	for _, name := range names {
		results = append(results, extractField(name, fields[name], lines))
	}
	return results
}

func extractField(name, fieldType string, lines []string) FieldResult {
	result := FieldResult{Field: name, Type: fieldType, Status: FieldNotOnPage}
	lowerName := strings.ToLower(name)
	for i, line := range lines {
		idx := strings.Index(strings.ToLower(line), lowerName)
		if idx < 0 {
			continue
		}
		// First matching line wins; everything after the field name on
		// that line is the candidate value.
		remainder := strings.TrimSpace(line[idx+len(name):])
		remainder = strings.TrimLeft(remainder, ":= \t\"'")
		value, ok := parseFieldValue(remainder, fieldType)
		result.Line = i + 1
		result.Source = line
		if !ok {
			result.Status = FieldUncertain
			return result
		}
		result.Status = FieldFound
		result.Value = value
		return result
	}
	return result
}

func parseFieldValue(remainder, fieldType string) (any, bool) {
	switch fieldType {
	case "string":
		if remainder == "" {
			return nil, false
		}
		// A string value runs to end of line, minus trailing quotes.
		return strings.TrimRight(remainder, "\"'"), true
	case "number":
		token := numberToken.FindString(remainder)
		if token == "" {
			return nil, false
		}
		cleaned := strings.ReplaceAll(token, ",", "")
		if strings.Contains(cleaned, ".") {
			if value, err := strconv.ParseFloat(cleaned, 64); err == nil {
				return value, true
			}
			return nil, false
		}
		if value, err := strconv.ParseInt(cleaned, 10, 64); err == nil {
			return value, true
		}
		return nil, false
	case "boolean":
		fields := strings.FieldsFunc(remainder, func(r rune) bool {
			return r == ' ' || r == '\t' || r == ',' || r == ';'
		})
		if len(fields) == 0 {
			return nil, false
		}
		switch strings.ToLower(strings.Trim(fields[0], "\"'")) {
		case "true", "yes":
			return true, true
		case "false", "no":
			return false, true
		}
		return nil, false
	default:
		return nil, false
	}
}

// ValidFieldType reports whether a field type is supported.
func ValidFieldType(fieldType string) bool {
	switch fieldType {
	case "string", "number", "boolean":
		return true
	default:
		return false
	}
}
