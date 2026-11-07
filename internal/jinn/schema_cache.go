package jinn

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Schema parsing cache (Rec 2.1).
//
// The embedded Schema bytes are immutable for the process lifetime. Parsing
// them once per process removes repeated Unmarshal/traversal on the hot
// Dispatch path. Cached structures are read-only; callers must not mutate
// the returned slice/maps.

type parsedSchemaResult struct {
	tools  []schemaTool
	byName map[string]schemaTool
	names  []string
	err    error
}

var parsedSchemaCache = sync.OnceValue(func() parsedSchemaResult {
	var tools []schemaTool
	if err := json.Unmarshal([]byte(Schema), &tools); err != nil {
		return parsedSchemaResult{err: fmt.Errorf("parse schema tools: %w", err)}
	}
	byName := make(map[string]schemaTool, len(tools))
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		byName[tool.Function.Name] = tool
		names = append(names, tool.Function.Name)
	}
	return parsedSchemaResult{tools: tools, byName: byName, names: names}
})

func parsedSchemaTools() ([]schemaTool, error) {
	result := parsedSchemaCache()
	if result.err != nil {
		return nil, result.err
	}
	return result.tools, nil
}

func schemaToolByName(name string) (schemaTool, bool, error) {
	result := parsedSchemaCache()
	if result.err != nil {
		return schemaTool{}, false, result.err
	}
	tool, ok := result.byName[name]
	return tool, ok, nil
}

func schemaToolNamesCached() ([]string, error) {
	result := parsedSchemaCache()
	return result.names, result.err
}
