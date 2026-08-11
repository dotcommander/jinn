package mcpsnapshot

import (
	"bytes"
	"encoding/json"
	"io"
	"sort"
	"strconv"
)

const changeToolChanged = "tool_changed"

// DiffChange is one user-visible approval drift class.
type DiffChange struct {
	Kind    string   `json:"kind"`
	Tool    string   `json:"tool,omitempty"`
	Paths   []string `json:"paths,omitempty"`
	Message string   `json:"message"`
}

// Diff compares a current snapshot candidate with its approved predecessor.
func Diff(approved, current Snapshot) ([]DiffChange, error) {
	changes := make([]DiffChange, 0)
	if approved.TargetFingerprint != current.TargetFingerprint {
		changes = append(changes, DiffChange{Kind: "target_configuration_changed", Message: "target configuration changed"})
	}
	manifestChangeStart := len(changes)
	if paths := differingRawPaths(approved.Server, current.Server); len(paths) != 0 {
		changes = append(changes, DiffChange{Kind: "server_changed", Paths: paths, Message: "server identity changed"})
	}
	oldTools, err := toolMap(approved.Tools)
	if err != nil {
		return nil, err
	}
	newTools, err := toolMap(current.Tools)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(oldTools)+len(newTools))
	seen := make(map[string]struct{}, len(oldTools)+len(newTools))
	for name := range oldTools {
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for name := range newTools {
		if _, exists := seen[name]; !exists {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		oldTool, oldExists := oldTools[name]
		newTool, newExists := newTools[name]
		switch {
		case !oldExists:
			changes = append(changes, DiffChange{Kind: "tool_added", Tool: name, Message: "tool added"})
		case !newExists:
			changes = append(changes, DiffChange{Kind: "tool_removed", Tool: name, Message: "tool removed"})
		default:
			paths := differingPaths(oldTool, newTool, "")
			if len(paths) != 0 {
				changes = append(changes, DiffChange{Kind: changeToolChanged, Tool: name, Paths: paths, Message: "tool changed"})
			}
		}
	}
	if approved.ManifestFingerprint != current.ManifestFingerprint && len(changes) == manifestChangeStart {
		changes = append(changes, DiffChange{Kind: "manifest_changed", Paths: []string{""}, Message: "manifest changed"})
	}
	return changes, nil
}

func toolMap(tools []json.RawMessage) (map[string]any, error) {
	output := make(map[string]any, len(tools))
	for _, raw := range tools {
		decoded, err := decodeJSONValue(raw)
		if err != nil {
			return nil, err
		}
		value, ok := decoded.(map[string]any)
		if !ok {
			return nil, &invalidToolError{}
		}
		name, _ := value["name"].(string)
		if name == "" {
			return nil, &invalidToolError{}
		}
		if _, exists := output[name]; exists {
			return nil, &invalidToolError{}
		}
		output[name] = value
	}
	return output, nil
}

type invalidToolError struct{}

func (*invalidToolError) Error() string { return "invalid MCP snapshot tool" }

func differingPaths(left, right any, path string) []string {
	if paths, compared := differingObjectPaths(left, right, path); compared {
		return paths
	}
	if paths, compared := differingArrayPaths(left, right, path); compared {
		return paths
	}
	return differingScalarPaths(left, right, path)
}

func differingObjectPaths(left, right any, path string) ([]string, bool) {
	leftObject, leftIsObject := left.(map[string]any)
	rightObject, rightIsObject := right.(map[string]any)
	if !leftIsObject || !rightIsObject {
		return nil, false
	}
	keys := make([]string, 0, len(leftObject)+len(rightObject))
	seen := make(map[string]struct{}, len(leftObject)+len(rightObject))
	for key := range leftObject {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range rightObject {
		if _, exists := seen[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	paths := make([]string, 0)
	for _, key := range keys {
		leftValue, leftExists := leftObject[key]
		rightValue, rightExists := rightObject[key]
		childPath := path + "/" + jsonPointerToken(key)
		if !leftExists || !rightExists {
			paths = append(paths, childPath)
			continue
		}
		paths = append(paths, differingPaths(leftValue, rightValue, childPath)...)
	}
	return paths, true
}

func differingArrayPaths(left, right any, path string) ([]string, bool) {
	leftArray, leftIsArray := left.([]any)
	rightArray, rightIsArray := right.([]any)
	if !leftIsArray || !rightIsArray {
		return nil, false
	}
	length := len(leftArray)
	if len(rightArray) > length {
		length = len(rightArray)
	}
	paths := make([]string, 0)
	for index := 0; index < length; index++ {
		childPath := path + "/" + strconv.Itoa(index)
		if index >= len(leftArray) || index >= len(rightArray) {
			paths = append(paths, childPath)
			continue
		}
		paths = append(paths, differingPaths(leftArray[index], rightArray[index], childPath)...)
	}
	return paths, true
}

func differingScalarPaths(left, right any, path string) []string {
	leftJSON, leftErr := CanonicalJSON(left)
	rightJSON, rightErr := CanonicalJSON(right)
	if leftErr != nil || rightErr != nil || string(leftJSON) != string(rightJSON) {
		if path == "" {
			return []string{""}
		}
		return []string{path}
	}
	return nil
}

func differingRawPaths(left, right json.RawMessage) []string {
	leftValue, leftErr := decodeJSONValue(left)
	rightValue, rightErr := decodeJSONValue(right)
	if leftErr != nil || rightErr != nil {
		return []string{""}
	}
	return differingPaths(leftValue, rightValue, "")
}

func decodeJSONValue(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, &invalidToolError{}
	}
	return value, nil
}
