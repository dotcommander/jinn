package jinn

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
)

const (
	memoryMaxValueBytes = 16384 // 16 KiB per value
)

var validKeyRe = regexp.MustCompile(`^[a-zA-Z0-9_.\\-]{1,128}$`)

// validateKey checks key charset and length.
func validateKey(key string) error {
	if key == "" || !validKeyRe.MatchString(key) {
		return &ErrWithSuggestion{
			Err:        fmt.Errorf("invalid key: %q — only [a-zA-Z0-9_.-] allowed, 1-128 chars", key),
			Suggestion: `use only letters, digits, underscores, dots, and hyphens in key names`,
		}
	}
	return nil
}

// scopeArg extracts the optional "scope" argument as a string.
func scopeArg(args map[string]interface{}) string {
	s, _ := args["scope"].(string)
	return s
}

// memoryTool implements the memory tool: save/recall/list/forget.
func (e *Engine) memoryTool(ctx context.Context, args map[string]interface{}) (string, error) {
	action, _ := args["action"].(string)
	switch action {
	case "save":
		return e.memorySave(ctx, args)
	case "recall":
		return e.memoryRecall(ctx, args)
	case "list":
		return e.memoryList(ctx, args)
	case "forget":
		return e.memoryForget(ctx, args)
	default:
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("unknown action: %q", action),
			Suggestion: `use action="save", "recall", "list", or "forget"`,
		}
	}
}

func (e *Engine) memorySave(ctx context.Context, args map[string]interface{}) (string, error) {
	key, _ := args["key"].(string)
	if err := validateKey(key); err != nil {
		return "", err
	}
	value, _ := args["value"].(string)
	if len(value) > memoryMaxValueBytes {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("value exceeds 16 KiB limit (%d bytes)", len(value)),
			Suggestion: "trim the value or split it across multiple keys",
		}
	}
	scope, err := e.resolveScope(scopeArg(args))
	if err != nil {
		return "", err
	}
	if err := e.memorySaveScoped(ctx, scope, key, value); err != nil {
		return "", err
	}
	return "saved: " + key, nil
}

func (e *Engine) memoryRecall(ctx context.Context, args map[string]interface{}) (string, error) {
	key, _ := args["key"].(string)
	if err := validateKey(key); err != nil {
		return "", err
	}
	scope, err := e.resolveScope(scopeArg(args))
	if err != nil {
		return "", err
	}
	val, found, err := e.memoryRecallScoped(ctx, scope, key)
	if err != nil {
		return "", err
	}
	if !found {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("key not found: %s", key),
			Suggestion: `use action="list" to see available keys`,
		}
	}
	return val, nil
}

func (e *Engine) memoryList(ctx context.Context, args map[string]interface{}) (string, error) {
	scope, err := e.resolveScope(scopeArg(args))
	if err != nil {
		return "", err
	}
	includeValues, _ := args["include_values"].(bool)
	if includeValues {
		entries, err := e.memoryListScopedWithValues(ctx, scope)
		if err != nil {
			return "", err
		}
		if entries == nil {
			entries = []memoryEntry{}
		}
		result := struct {
			Entries []memoryEntry `json:"entries"`
			Count   int           `json:"count"`
		}{entries, len(entries)}
		data, err := json.Marshal(result)
		if err != nil {
			return "", fmt.Errorf("memory: list marshal: %w", err)
		}
		return string(data), nil
	}
	keys, err := e.memoryListScoped(ctx, scope)
	if err != nil {
		return "", err
	}
	if keys == nil {
		keys = []string{}
	}
	result := struct {
		Keys  []string `json:"keys"`
		Count int      `json:"count"`
	}{keys, len(keys)}
	data, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("memory: list marshal: %w", err)
	}
	return string(data), nil
}

func (e *Engine) memoryForget(ctx context.Context, args map[string]interface{}) (string, error) {
	key, _ := args["key"].(string)
	if err := validateKey(key); err != nil {
		return "", err
	}
	scope, err := e.resolveScope(scopeArg(args))
	if err != nil {
		return "", err
	}
	if err := e.memoryForgetScoped(ctx, scope, key); err != nil {
		return "", err
	}
	return "forgotten: " + key, nil
}
