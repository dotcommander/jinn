package jinn

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// SchemaToolNames returns the ordered list of tool names declared in Schema.
// This is the single source of truth — list_tools and other introspection
// callers derive their tool list from here rather than maintaining a parallel
// slice that drifts as tools are added or renamed.
func SchemaToolNames() ([]string, error) {
	var raw []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal([]byte(Schema), &raw); err != nil {
		return nil, fmt.Errorf("parse schema for tool names: %w", err)
	}
	names := make([]string, 0, len(raw))
	for _, t := range raw {
		if t.Function.Name != "" {
			names = append(names, t.Function.Name)
		}
	}
	return names, nil
}

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
