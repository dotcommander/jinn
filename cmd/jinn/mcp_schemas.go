package main

import "github.com/dotcommander/jinn/internal/jinn"

//nolint:goconst // JSON Schema maps retain canonical wire keys beside their constraints.
var mcpRouteInputSchema = map[string]any{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"title":   "jinn_route input",
	"type":    "object",
	"properties": map[string]any{
		"need": map[string]any{
			"type":        "string",
			"description": "Concrete natural-language coding task or capability request.",
		},
		"max_tools": map[string]any{
			"type":        "integer",
			"description": "Maximum recommendations; omitted enables adaptive one-or-two routing.",
			"minimum":     1,
			"maximum":     jinn.RouteMaxTools,
		},
		"include_schema": map[string]any{
			"type":        "boolean",
			"description": "Include lean schemas only for returned tools.",
			"default":     false,
		},
		"include_signature": map[string]any{
			"type":        "boolean",
			"description": "Include compact input signatures only for returned tools.",
			"default":     false,
		},
		"include_call": map[string]any{
			"type":        "boolean",
			"description": "Include one minimal argument template.",
			"default":     false,
		},
		"include_mutating": map[string]any{
			"type":        "boolean",
			"description": "Allow recommendations for mutating tools.",
			"default":     true,
		},
		"include_network": map[string]any{
			"type": "boolean", "description": "Allow recommendations for public-network tools.", "default": true,
		},
	},
	"required":             []string{"need"},
	"additionalProperties": false,
}

func mcpRouteInputSchemaForProfile(profile mcpProfile) map[string]any {
	if profile != mcpProfileReadOnly && profile != mcpProfileNetwork {
		return mcpRouteInputSchema
	}
	schema := make(map[string]any, len(mcpRouteInputSchema))
	for key, value := range mcpRouteInputSchema {
		schema[key] = value
	}
	properties := mcpRouteInputSchema["properties"].(map[string]any)
	readOnlyProperties := make(map[string]any, len(properties))
	for key, value := range properties {
		readOnlyProperties[key] = value
	}
	includeMutating := properties["include_mutating"].(map[string]any)
	readOnlyIncludeMutating := make(map[string]any, len(includeMutating))
	for key, value := range includeMutating {
		readOnlyIncludeMutating[key] = value
	}
	readOnlyIncludeMutating["default"] = false
	readOnlyIncludeMutating["description"] = "Always false in this non-mutating profile; mutating tools are never recommended."
	readOnlyProperties["include_mutating"] = readOnlyIncludeMutating
	includeNetwork := properties["include_network"].(map[string]any)
	readOnlyIncludeNetwork := make(map[string]any, len(includeNetwork))
	for key, value := range includeNetwork {
		readOnlyIncludeNetwork[key] = value
	}
	if profile == mcpProfileReadOnly {
		readOnlyIncludeNetwork["default"] = false
		readOnlyIncludeNetwork["description"] = "Always false in the read-only profile; public-network tools are never recommended."
	}
	readOnlyProperties["include_network"] = readOnlyIncludeNetwork
	schema["properties"] = readOnlyProperties
	return schema
}

//nolint:gochecknoglobals,goconst // MCP schemas are immutable package-level wire contracts.
var mcpCallInputSchema = map[string]any{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"title":   "jinn_call input",
	"type":    "object",
	"properties": map[string]any{
		"tool": map[string]any{
			"type":        "string",
			"description": "Read-only Jinn tool to execute. Call jinn_route first to choose one.",
			"enum":        jinn.ReadOnlyToolNames(),
		},
		"arguments": map[string]any{
			"type":                 "object",
			"description":          "Arguments for the selected read-only Jinn tool.",
			"additionalProperties": true,
		},
		"compress": map[string]any{
			"type":        "boolean",
			"description": "Apply Jinn's deterministic context compression to text output.",
			"default":     true,
		},
		"route_id": map[string]any{
			"type":        "string",
			"description": "Route identifier returned by jinn_route.",
			"pattern":     "^[0-9a-f]{32}$",
		},
	},
	"required":             []string{"tool"},
	"additionalProperties": false,
}

func mcpCallInputSchemaForProfile(profile mcpProfile) map[string]any {
	if profile != mcpProfileNetwork {
		return mcpCallInputSchema
	}
	schema := make(map[string]any, len(mcpCallInputSchema))
	for key, value := range mcpCallInputSchema {
		schema[key] = value
	}
	properties := make(map[string]any, len(mcpCallInputSchema["properties"].(map[string]any)))
	for key, value := range mcpCallInputSchema["properties"].(map[string]any) {
		properties[key] = value
	}
	tool := make(map[string]any, len(properties["tool"].(map[string]any)))
	for key, value := range properties["tool"].(map[string]any) {
		tool[key] = value
	}
	allow := append(jinn.ReadOnlyToolNames(), jinn.NetworkToolNames()...)
	tool["enum"] = allow
	tool["description"] = "Read-only local or explicitly networked Jinn tool to execute. Call jinn_route first to choose one."
	properties["tool"] = tool
	compress := make(map[string]any, len(properties["compress"].(map[string]any)))
	for key, value := range properties["compress"].(map[string]any) {
		compress[key] = value
	}
	delete(compress, "default")
	compress["description"] = "Apply Jinn's deterministic context compression to text output. In the network profile, local read-only tools default to true and web_fetch/web_search default to false; set explicitly to override."
	properties["compress"] = compress
	schema["properties"] = properties
	return schema
}

//nolint:gochecknoglobals,goconst // MCP schemas are immutable package-level wire contracts.
var mcpCallOutputSchema = map[string]any{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"title":   "jinn_call output",
	"type":    "object",
	"properties": map[string]any{
		"tool": map[string]any{
			"type": "string",
		},
		"result": map[string]any{
			"type": "string",
		},
		"content": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":     map[string]any{"type": "string"},
					"text":     map[string]any{"type": "string"},
					"data":     map[string]any{"type": "string"},
					"mimeType": map[string]any{"type": "string"},
				},
				"required":             []string{"type"},
				"additionalProperties": false,
			},
		},
		"meta": map[string]any{
			"type":                 "object",
			"additionalProperties": true,
		},
		"route_id":   map[string]any{"type": "string"},
		"error":      map[string]any{"type": "string"},
		"error_code": map[string]any{"type": "string"},
		"suggestion": map[string]any{"type": "string"},
		"retryable":  map[string]any{"type": "boolean"},
		"next_call":  map[string]any{"$ref": "#/$defs/next_call"},
	},
	"required":             []string{"tool"},
	"additionalProperties": false,
	"$defs": map[string]any{
		"next_call": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tool":      map[string]any{"type": "string"},
				"arguments": map[string]any{"type": "object"},
			},
			"required":             []string{"tool", "arguments"},
			"additionalProperties": false,
		},
	},
}

//nolint:goconst // JSON Schema maps retain canonical wire keys beside their constraints.
var mcpRouteOutputSchema = map[string]any{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"title":   "jinn_route output",
	"type":    "object",
	"properties": map[string]any{
		"route_id": map[string]any{"type": "string"},
		"query": map[string]any{
			"type": "string",
		},
		"matches": map[string]any{
			"type":  "array",
			"items": map[string]any{"$ref": "#/$defs/route_match"},
		},
		"notes": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
		},
		"confidence":   map[string]any{"type": "string", "enum": []string{"none", "low", "ambiguous", "high"}},
		"score_margin": map[string]any{"type": "integer"},
		"adaptive":     map[string]any{"type": "boolean"},
	},
	"required":             []string{"query", "confidence", "adaptive", "matches", "notes"},
	"additionalProperties": false,
	"$defs": map[string]any{
		"route_match": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"reason":      map[string]any{"type": "string"},
				"mutating":    map[string]any{"type": "boolean"},
				"risk":        map[string]any{"type": "string"},
				"features": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "string"},
				},
				"schema":    map[string]any{},
				"signature": map[string]any{"type": "string"},
				"call": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tool":      map[string]any{"type": "string"},
						"arguments": map[string]any{"type": "object"},
						"replace":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					},
					"required":             []string{"tool", "arguments"},
					"additionalProperties": false,
				},
			},
			"required":             []string{"name", "description", "reason", "mutating", "risk"},
			"additionalProperties": false,
		},
	},
}
