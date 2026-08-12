package jinn

import (
	"sort"
	"strings"
)

func routeCallTemplate(tool schemaTool) *RouteCallTemplate {
	replace := make([]string, 0)
	arguments := requiredObjectTemplate(tool.Function.Parameters, "", &replace)
	return &RouteCallTemplate{Tool: tool.Function.Name, Arguments: arguments, Replace: replace}
}

func requiredObjectTemplate(schema map[string]any, prefix string, replace *[]string) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	required := stringSlice(schema["required"])
	sort.Strings(required)
	result := make(map[string]any, len(required))
	for _, name := range required {
		property, _ := properties[name].(map[string]any)
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		result[name] = schemaTemplateValue(property, path, replace)
	}
	return result
}

func schemaTemplateValue(schema map[string]any, path string, replace *[]string) any {
	if value, ok := schema["default"]; ok {
		return cloneJSONValue(value)
	}
	if values, ok := schema["enum"].([]any); ok && len(values) > 0 {
		return cloneJSONValue(values[0])
	}
	switch schema["type"] {
	case "object":
		result := requiredObjectTemplate(schema, path, replace)
		if len(result) == 0 && path != "" {
			*replace = append(*replace, strings.TrimPrefix(path, "."))
			return "<required>"
		}
		return result
	case "array":
		items, _ := schema["items"].(map[string]any)
		if minimum, _ := schema["minItems"].(float64); minimum > 0 {
			return []any{schemaTemplateValue(items, path+"[]", replace)}
		}
		return []any{}
	case "boolean":
		return false
	case "integer", "number":
		if minimum, ok := schema["minimum"].(float64); ok {
			return minimum
		}
		return 0
	default:
		*replace = append(*replace, strings.TrimPrefix(path, "."))
		return "<required>"
	}
}

func stringSlice(raw any) []string {
	values, _ := raw.([]any)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
