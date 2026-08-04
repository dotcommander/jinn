package jinn

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// SchemaToolNames returns the ordered tool names declared by the embedded wire
// schema. The runtime registry validates exact parity with this declaration.
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
	return LeanSchemaForMode(ShellModeUnsafe)
}

// LeanSchemaForMode returns the prompt-facing schema for an execution policy.
func LeanSchemaForMode(mode ShellMode) (string, error) {
	var schema any
	if err := json.Unmarshal([]byte(Schema), &schema); err != nil {
		return "", err
	}
	stripParameterDescriptions(schema)
	closeSchemaObjects(schema)
	if mode == ShellModeDisabled {
		items, _ := schema.([]any)
		filtered := items[:0]
		for _, item := range items {
			tool, _ := item.(map[string]any)
			fn, _ := tool["function"].(map[string]any)
			if fn["name"] == "run_shell" {
				continue
			}
			removeToolEnum(fn, "run_shell")
			filtered = append(filtered, item)
		}
		schema = filtered
		stripShellPlanOperations(schema)
	}
	out, err := json.Marshal(schema)
	if err != nil {
		return "", fmt.Errorf("marshal lean schema: %w", err)
	}
	return string(out), nil
}

// stripShellPlanOperations removes the shell-shaped branch from run_plan's
// nested command union. Removing run_shell from the outer catalog alone would
// leave a structurally valid route to shell execution in a disabled schema.
func stripShellPlanOperations(v any) {
	switch node := v.(type) {
	case []any:
		for _, child := range node {
			stripShellPlanOperations(child)
		}
	case map[string]any:
		if properties, ok := node["properties"].(map[string]any); ok {
			delete(properties, "shell")
		}
		if branches, ok := node["oneOf"].([]any); ok {
			kept := branches[:0]
			for _, branch := range branches {
				if schemaBranchHasShell(branch) {
					continue
				}
				kept = append(kept, branch)
			}
			node["oneOf"] = kept
		}
		for _, child := range node {
			stripShellPlanOperations(child)
		}
	}
}

func schemaBranchHasShell(v any) bool {
	node, ok := v.(map[string]any)
	if !ok {
		return false
	}
	properties, _ := node["properties"].(map[string]any)
	if _, hasShell := properties["shell"]; hasShell {
		return true
	}
	if required, ok := node["required"].([]any); ok {
		for _, field := range required {
			if field == "shell" {
				return true
			}
		}
	}
	return false
}

func closeSchemaObjects(v any) {
	switch node := v.(type) {
	case []any:
		for _, child := range node {
			closeSchemaObjects(child)
		}
	case map[string]any:
		if node["type"] == "object" {
			if _, hasProperties := node["properties"]; hasProperties {
				node["additionalProperties"] = false
			}
		}
		for _, child := range node {
			closeSchemaObjects(child)
		}
	}
}

func removeToolEnum(v any, name string) {
	switch node := v.(type) {
	case []any:
		for _, child := range node {
			removeToolEnum(child, name)
		}
	case map[string]any:
		if values, ok := node["enum"].([]any); ok {
			kept := values[:0]
			for _, value := range values {
				if value != name {
					kept = append(kept, value)
				}
			}
			node["enum"] = kept
		}
		for _, child := range node {
			removeToolEnum(child, name)
		}
	}
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
