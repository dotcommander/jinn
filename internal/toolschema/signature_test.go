package toolschema

import "testing"

func TestRender(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
		want   string
	}{
		{"required_then_sorted_optional", map[string]any{"type": "object", "properties": map[string]any{"z": map[string]any{"type": "boolean"}, "a": map[string]any{"type": "string"}, "b": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}}, "required": []any{"z", "a"}, "additionalProperties": false}, "tool(a:string,z:boolean,b?:integer[])"},
		{"enum_and_default", map[string]any{"type": "object", "properties": map[string]any{"mode": map[string]any{"type": "string", "enum": []any{"fast", "safe"}}, "limit": map[string]any{"type": "integer", "default": 10}}, "additionalProperties": false}, "tool(limit?:integer=10,mode?:enum(fast|safe))"},
		{"open_object", map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": true}, "tool(...:json)"},
		{"implicit_open_object", map[string]any{"type": "object", "properties": map[string]any{}}, "tool(...:json)"},
		{"missing_required_property", map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{"unknown"}, "additionalProperties": false}, "tool(unknown:json)"},
		{"nullable_falls_back", map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": []any{"string", "null"}}}, "additionalProperties": false}, "tool(value?:json)"},
		{"malformed_falls_back", map[string]any{"type": "object", "properties": "bad"}, "tool(json)"},
		{"control_and_delimiter_names", map[string]any{"type": "object", "properties": map[string]any{"comma,name": map[string]any{"type": "string"}, "line\nbreak": map[string]any{"type": "number"}}, "additionalProperties": false}, "tool(\"comma,name\"?:string,\"line\\nbreak\"?:number)"},
		{"unicode_and_duplicate_looking_names", map[string]any{"type": "object", "properties": map[string]any{"é": map[string]any{"type": "string"}, "a:b": map[string]any{"type": "boolean"}}, "additionalProperties": false}, "tool(\"a:b\"?:boolean,\"é\"?:string)"},
		{"composition_and_large_values", map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"oneOf": []any{}}, "y": map[string]any{"type": "string", "default": "abcdefghijklmnopqrstuvwxyz0123456789"}, "z": map[string]any{"type": "string", "enum": []any{"abcdefghijklmnopqrstuvwxyz012345678901234567890123456789"}}, "nulls": map[string]any{"type": "array", "items": map[string]any{"type": "null"}}}, "additionalProperties": false}, "tool(nulls?:null[],x?:json,y?:string,z?:string)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Render("tool", tt.schema); got != tt.want {
				t.Fatalf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}
