package mcpbatch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/voocel/mcp-sdk-go/protocol"
)

// Validate confirms every call against approved and current read-only tools.
// It runs before Execute so no partially validated batch can invoke a tool.
func Validate(input Input, approved, current []*protocol.Tool) error {
	approvedByName, err := toolsByName(approved)
	if err != nil {
		return fmt.Errorf("approved snapshot: %w", err)
	}
	currentByName, err := toolsByName(current)
	if err != nil {
		return fmt.Errorf("current server manifest: %w", err)
	}
	var validationErrors []error
	for _, call := range input.Calls {
		approvedTool, approvedOK := approvedByName[call.Tool]
		currentTool, currentOK := currentByName[call.Tool]
		if !approvedOK || !currentOK {
			validationErrors = append(validationErrors, fmt.Errorf("batch call %q references unapproved tool %q", call.ID, call.Tool))
			continue
		}
		if !isReadOnly(approvedTool) || !isReadOnly(currentTool) {
			validationErrors = append(validationErrors, fmt.Errorf("batch call %q tool %q must advertise readOnlyHint=true and not destructiveHint=true", call.ID, call.Tool))
			continue
		}
		arguments := call.Arguments
		if arguments == nil {
			arguments = map[string]any{}
		}
		if err := validateArguments(approvedTool, arguments); err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("batch call %q arguments: %w", call.ID, err))
		}
	}
	return errors.Join(validationErrors...)
}

func toolsByName(tools []*protocol.Tool) (map[string]*protocol.Tool, error) {
	byName := make(map[string]*protocol.Tool, len(tools))
	for _, tool := range tools {
		if tool == nil || tool.Name == "" {
			return nil, errors.New("tool has no name")
		}
		if _, exists := byName[tool.Name]; exists {
			return nil, fmt.Errorf("duplicate tool %q", tool.Name)
		}
		byName[tool.Name] = tool
	}
	return byName, nil
}

func isReadOnly(tool *protocol.Tool) bool {
	return tool.Annotations != nil && tool.Annotations.ReadOnlyHint != nil && *tool.Annotations.ReadOnlyHint && (tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint)
}

func validateArguments(tool *protocol.Tool, arguments map[string]any) error {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(noExternalSchemaLoader{})
	location := "urn:jinn:mcp-batch:" + tool.Name
	schemaDocument, err := schemaDocument(tool.InputSchema)
	if err != nil {
		return err
	}
	if addErr := compiler.AddResource(location, schemaDocument); addErr != nil {
		return fmt.Errorf("register approved schema: %w", addErr)
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		return fmt.Errorf("compile approved schema: %w", err)
	}
	if err := schema.Validate(arguments); err != nil {
		return fmt.Errorf("do not satisfy approved schema: %w", err)
	}
	return nil
}

func schemaDocument(schema protocol.JSONSchema) (any, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode approved schema: %w", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode approved schema: %w", err)
	}
	return document, nil
}

type noExternalSchemaLoader struct{}

func (noExternalSchemaLoader) Load(location string) (any, error) {
	return nil, fmt.Errorf("external JSON Schema reference %q is not permitted", location)
}
