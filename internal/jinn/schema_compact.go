package jinn

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// CompactSchema returns Schema without insignificant JSON whitespace.
func CompactSchema() (string, error) {
	var out bytes.Buffer
	if err := json.Compact(&out, []byte(Schema)); err != nil {
		return "", err
	}
	return out.String(), nil
}

// LeanSchema returns a prompt-facing schema that keeps tool descriptions but
// removes nested parameter descriptions. Parameter names, types, defaults,
// enums, oneOf branches, and required fields remain intact.
func LeanSchema() (string, error) {
	var schema any
	if err := json.Unmarshal([]byte(Schema), &schema); err != nil {
		return "", err
	}
	stripParameterDescriptions(schema)
	out, err := json.Marshal(schema)
	if err != nil {
		return "", fmt.Errorf("marshal lean schema: %w", err)
	}
	return string(out), nil
}

func stripParameterDescriptions(v any) {
	switch node := v.(type) {
	case []any:
		for _, item := range node {
			stripParameterDescriptions(item)
		}
	case map[string]any:
		if !isFunctionDefinition(node) {
			delete(node, "description")
		}
		for _, item := range node {
			stripParameterDescriptions(item)
		}
	}
}

func isFunctionDefinition(node map[string]any) bool {
	_, hasName := node["name"]
	_, hasDescription := node["description"]
	_, hasParameters := node["parameters"]
	return hasName && hasDescription && hasParameters
}
