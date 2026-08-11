package jinn

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// validateToolArgs applies the registry-owned wire schema at every dispatch
// boundary, including inspector calls and nested run_plan tool operations.
func validateToolArgs(tool string, args map[string]interface{}) error {
	if args == nil {
		args = map[string]interface{}{}
	}
	if err := validateSuppliedNonNegativeNumbers(args); err != nil {
		return &ErrWithSuggestion{Err: fmt.Errorf("%s: %w", tool, err), Suggestion: "omit optional numeric fields to use their defaults, or provide a non-negative value where supported", Code: ErrCodeInvalidArgs}
	}
	tools, err := parseSchemaTools()
	if err != nil {
		return fmt.Errorf("validate %s args: %w", tool, err)
	}
	for _, candidate := range tools {
		if candidate.Function.Name != tool {
			continue
		}
		if err := validateSchemaValue("args", args, candidate.Function.Parameters); err != nil {
			return &ErrWithSuggestion{
				Err:        fmt.Errorf("%s: %w", tool, err),
				Suggestion: "use list_tools with include_schema=true to inspect the accepted arguments",
				Code:       ErrCodeInvalidArgs,
			}
		}
		return nil
	}
	return fmt.Errorf("unknown tool: %s", tool)
}

// Numeric defaults are represented by omitted fields. Supplied negative values
// remain invalid, while zero is allowed where the schema or tool semantics
// define it as a default or empty limit.
func validateSuppliedNonNegativeNumbers(args map[string]interface{}) error {
	numeric := map[string]bool{
		"timeout": true, "max_matches": true, "max_entries": true,
		"limit": true, "max_results": true, "depth": true,
		"max_depth": true, "context_lines": true, "highlight_sentences": true,
	}
	for key, value := range args {
		if !numeric[key] {
			continue
		}
		if number, ok := value.(float64); ok && number < 0 {
			return fmt.Errorf("args.%s must be non-negative when supplied", key)
		}
	}
	return nil
}

//nolint:funlen,gocognit,gocyclo,revive // this mirrors the schema's recursive union/type/object/array validation rules.
func validateSchemaValue(path string, value any, schema map[string]any) error {
	if branches, ok := schema["oneOf"].([]any); ok && len(branches) > 0 {
		matches := 0
		for _, rawBranch := range branches {
			branch, _ := rawBranch.(map[string]any)
			if validateSchemaValue(path, value, branch) == nil {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%s does not match exactly one accepted shape", path)
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		matched := false
		for _, allowed := range enum {
			if jsonScalarEqual(value, allowed) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s has invalid value %v", path, value)
		}
	}
	if typ, _ := schema["type"].(string); typ != "" {
		if err := validateJSONType(path, value, typ); err != nil {
			return err
		}
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		properties, hasProperties := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]any); ok {
			for _, raw := range required {
				key, _ := raw.(string)
				if _, exists := typed[key]; !exists {
					return fmt.Errorf("%s.%s is required", path, key)
				}
			}
		}
		if hasProperties {
			for key, child := range typed {
				rawSchema, exists := properties[key]
				if !exists {
					return fmt.Errorf("%s contains unknown field %q", path, key)
				}
				childSchema, _ := rawSchema.(map[string]any)
				if err := validateSchemaValue(path+"."+key, child, childSchema); err != nil {
					return err
				}
			}
		}
		if !hasProperties {
			if additional, ok := schema["additionalProperties"].(map[string]any); ok {
				for key, child := range typed {
					if err := validateSchemaValue(path+"."+key, child, additional); err != nil {
						return err
					}
				}
			}
		}
	case []interface{}:
		if min, ok := schema["minItems"].(float64); ok && len(typed) < int(min) {
			return fmt.Errorf("%s must contain at least %d items", path, int(min))
		}
		if max, ok := schema["maxItems"].(float64); ok && len(typed) > int(max) {
			return fmt.Errorf("%s must contain at most %d items", path, int(max))
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for index, child := range typed {
				if err := validateSchemaValue(fmt.Sprintf("%s[%d]", path, index), child, itemSchema); err != nil {
					return err
				}
			}
		}
	case string:
		if min, ok := schema["minLength"].(float64); ok && len(typed) < int(min) {
			return fmt.Errorf("%s must not be empty", path)
		}
	}
	if number, ok := value.(float64); ok {
		if minimum, ok := schema["minimum"].(float64); ok && number < minimum {
			return fmt.Errorf("%s must be at least %v", path, minimum)
		}
		if maximum, ok := schema["maximum"].(float64); ok && number > maximum {
			return fmt.Errorf("%s must be at most %v", path, maximum)
		}
	}
	return nil
}

func validateJSONType(path string, value any, typ string) error {
	valid := false
	switch typ {
	case "object":
		_, valid = value.(map[string]interface{})
	case "array":
		_, valid = value.([]interface{})
	case "string":
		_, valid = value.(string)
	case "boolean":
		_, valid = value.(bool)
	case "number":
		_, valid = value.(float64)
	case "integer":
		if number, ok := value.(float64); ok {
			valid = !math.IsNaN(number) && !math.IsInf(number, 0) && number == math.Trunc(number)
		}
	case "null":
		valid = value == nil
	}
	if !valid {
		return fmt.Errorf("%s must be %s (got %s)", path, typ, jsonTypeName(value))
	}
	return nil
}

func jsonTypeName(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case map[string]interface{}:
		return "object"
	case []interface{}:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "number"
	default:
		return strings.TrimPrefix(fmt.Sprintf("%T", value), "interface {}")
	}
}

func jsonScalarEqual(left, right any) bool {
	l, _ := json.Marshal(left)
	r, _ := json.Marshal(right)
	return string(l) == string(r)
}
