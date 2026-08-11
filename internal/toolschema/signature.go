// Package toolschema renders compact, deterministic signatures for MCP tool input schemas.
package toolschema

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

var bareName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
var bareEnumValue = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

const (
	schemaTypeArray   = "array"
	schemaTypeBoolean = "boolean"
	schemaTypeInteger = "integer"
	schemaTypeNull    = "null"
	schemaTypeNumber  = "number"
	schemaTypeObject  = "object"
	schemaTypeString  = "string"
	signatureJSON     = "json"
)

// Render returns a compact signature for name and its JSON Schema input.
// Malformed and unsupported schema constructs deliberately fall back to json.
func Render(name string, schema map[string]any) string {
	properties, ok := schema["properties"].(map[string]any)
	if !ok && schema["properties"] != nil {
		return renderName(name) + "(" + signatureJSON + ")"
	}
	if schemaType, ok := schema["type"].(string); !ok || schemaType != schemaTypeObject {
		return renderName(name) + "(" + signatureJSON + ")"
	}
	required, validRequired := requiredNames(schema["required"])
	if !validRequired {
		return renderName(name) + "(" + signatureJSON + ")"
	}
	requiredSet := make(map[string]bool, len(required))
	for _, property := range required {
		requiredSet[property] = true
	}

	requiredParts := make([]string, 0, len(required))
	optionalParts := make([]string, 0, len(properties))
	for _, property := range required {
		requiredParts = append(requiredParts, renderProperty(property, properties[property], false))
	}
	optionalNames := make([]string, 0, len(properties))
	for property := range properties {
		if !requiredSet[property] {
			optionalNames = append(optionalNames, property)
		}
	}
	sort.Strings(optionalNames)
	for _, property := range optionalNames {
		optionalParts = append(optionalParts, renderProperty(property, properties[property], true))
	}
	parts := make([]string, 0, len(requiredParts)+len(optionalParts)+1)
	parts = append(parts, requiredParts...)
	parts = append(parts, optionalParts...)
	if additional, exists := schema["additionalProperties"]; !exists || additional != false {
		parts = append(parts, "...:"+signatureJSON)
	}
	return renderName(name) + "(" + strings.Join(parts, ",") + ")"
}

func requiredNames(value any) ([]string, bool) {
	if value == nil {
		return nil, true
	}
	raw, ok := value.([]any)
	if !ok {
		return nil, false
	}
	names := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, value := range raw {
		name, ok := value.(string)
		if !ok || seen[name] {
			return nil, false
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names, true
}

func renderProperty(name string, value any, optional bool) string {
	suffix := ":"
	if optional {
		suffix = "?:"
	}
	return renderName(name) + suffix + renderSchema(value)
}

func renderSchema(value any) string {
	schema, ok := value.(map[string]any)
	if !ok || hasComposition(schema) {
		return signatureJSON
	}
	if enum, enumOK := renderEnum(schema["enum"]); enumOK {
		return enum
	}
	typeName, ok := schema["type"].(string)
	if !ok {
		return signatureJSON
	}
	var rendered string
	switch typeName {
	case schemaTypeString, schemaTypeNumber, schemaTypeInteger, schemaTypeBoolean, schemaTypeNull, schemaTypeObject:
		rendered = typeName
	case schemaTypeArray:
		itemSchema, ok := schema["items"].(map[string]any)
		if !ok || hasComposition(itemSchema) {
			return signatureJSON
		}
		itemType, ok := itemSchema["type"].(string)
		if !ok || itemType == schemaTypeArray || itemType == schemaTypeObject {
			return signatureJSON
		}
		switch itemType {
		case schemaTypeString, schemaTypeNumber, schemaTypeInteger, schemaTypeBoolean, schemaTypeNull:
			rendered = itemType + "[]"
		default:
			return signatureJSON
		}
	default:
		return signatureJSON
	}
	if defaultValue, exists := schema["default"]; exists {
		if raw, ok := scalarJSON(defaultValue, 32); ok {
			rendered += "=" + raw
		}
	}
	return rendered
}

func hasComposition(schema map[string]any) bool {
	for _, key := range []string{"$ref", "allOf", "anyOf", "oneOf", "not", "if", "then", "else"} {
		if _, ok := schema[key]; ok {
			return true
		}
	}
	return false
}

func renderEnum(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	raw, ok := value.([]any)
	if !ok || len(raw) == 0 {
		return "", false
	}
	members := make([]string, 0, len(raw))
	for _, member := range raw {
		if !isScalar(member) {
			return "", false
		}
		members = append(members, renderEnumValue(member))
	}
	encoded, err := json.Marshal(raw)
	if err != nil || len(encoded) > 48 {
		return "", false
	}
	return "enum(" + strings.Join(members, "|") + ")", true
}

func renderEnumValue(value any) string {
	if text, ok := value.(string); ok && bareEnumValue.MatchString(text) {
		return text
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func scalarJSON(value any, maximum int) (string, bool) {
	if !isScalar(value) {
		return "", false
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > maximum {
		return "", false
	}
	return string(encoded), true
}

func isScalar(value any) bool {
	switch value.(type) {
	case nil, string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, json.Number:
		return true
	default:
		return false
	}
}

func renderName(name string) string {
	if bareName.MatchString(name) {
		return name
	}
	encoded, _ := json.Marshal(name)
	return string(encoded)
}
