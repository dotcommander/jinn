package jinn

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
)

const memoryMaxValueBytes = 16384 // 16 KiB per value

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

// validateKind checks that kind is one of fact|directive|lesson.
func validateKind(kind string) error {
	switch kind {
	case "fact", "directive", "lesson":
		return nil
	}
	return &ErrWithSuggestion{
		Err:        fmt.Errorf("invalid kind: %q — must be one of: fact, directive, lesson", kind),
		Suggestion: `use kind="fact" (default), "directive", or "lesson"`,
		Code:       ErrCodeInvalidArgs,
	}
}

// strArg extracts an optional string arg.
func strArg(args map[string]interface{}, key string) string {
	s, _ := args[key].(string)
	return s
}

// boolArg extracts an optional bool arg.
func boolArg(args map[string]interface{}, key string) bool {
	b, _ := args[key].(bool)
	return b
}

// memoryTool implements the memory tool: save|recall|list|forget|gc.
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
	case "gc":
		return e.memoryGCAction(ctx, args)
	default:
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("unknown action: %q", action),
			Suggestion: `use action="save", "recall", "list", "forget", or "gc"`,
		}
	}
}

func (e *Engine) memorySave(ctx context.Context, args map[string]interface{}) (string, error) {
	key := strArg(args, "key")
	if err := validateKey(key); err != nil {
		return "", err
	}
	value := strArg(args, "value")
	if len(value) > memoryMaxValueBytes {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("value exceeds 16 KiB limit (%d bytes)", len(value)),
			Suggestion: "trim the value or split it across multiple keys",
		}
	}

	kind := strArg(args, "kind")
	if kind == "" {
		kind = "fact"
	}
	if err := validateKind(kind); err != nil {
		return "", err
	}

	expiresAt, err := parseExpiresIn(strArg(args, "expires_in"))
	if err != nil {
		return "", err
	}

	rs, err := e.resolveMemoryScope(strArg(args, "scope"), strArg(args, "scope_id"))
	if err != nil {
		return "", err
	}

	pin := boolArg(args, "pin")
	agent := resolveAgent(args)
	requestID := strArg(args, "request_id")

	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", err
	}

	result, err := runIdempotent(ctx, db, agent, requestID, "memory.save", func(tx *sql.Tx) (any, error) {
		if uErr := memoryUpsertTx(ctx, tx, rs.scope, rs.scopeID, key, value, kind, pin, expiresAt); uErr != nil {
			return nil, uErr
		}
		return "saved: " + key, nil
	})
	if err != nil {
		return "", err
	}
	return result.(string), nil
}

func (e *Engine) memoryRecall(ctx context.Context, args map[string]interface{}) (string, error) {
	key := strArg(args, "key")
	if err := validateKey(key); err != nil {
		return "", err
	}
	rs, err := e.resolveMemoryScope(strArg(args, "scope"), strArg(args, "scope_id"))
	if err != nil {
		return "", err
	}
	val, found, err := e.memoryRecallScoped(ctx, rs.scope, rs.scopeID, key)
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
	rs, err := e.resolveMemoryScope(strArg(args, "scope"), strArg(args, "scope_id"))
	if err != nil {
		return "", err
	}
	if boolArg(args, "include_values") {
		entries, err := e.memoryListScopedWithValues(ctx, rs.scope, rs.scopeID)
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
	keys, err := e.memoryListScoped(ctx, rs.scope, rs.scopeID)
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
	key := strArg(args, "key")
	if err := validateKey(key); err != nil {
		return "", err
	}
	rs, err := e.resolveMemoryScope(strArg(args, "scope"), strArg(args, "scope_id"))
	if err != nil {
		return "", err
	}
	agent := resolveAgent(args)
	requestID := strArg(args, "request_id")

	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", err
	}

	result, err := runIdempotent(ctx, db, agent, requestID, "memory.forget", func(tx *sql.Tx) (any, error) {
		if _, delErr := tx.ExecContext(ctx,
			"DELETE FROM memory WHERE scope=? AND scope_id=? AND key=?",
			rs.scope, rs.scopeID, key,
		); delErr != nil {
			return nil, fmt.Errorf("memory: forget: %w", delErr)
		}
		return "forgotten: " + key, nil
	})
	if err != nil {
		return "", err
	}
	return result.(string), nil
}

func (e *Engine) memoryGCAction(ctx context.Context, args map[string]interface{}) (string, error) {
	// scope arg is optional: when supplied, restrict gc to that scope.
	// We only pass the scope string (not scope_id) — gc sweeps the whole scope bucket.
	gcScope := ""
	if s := strArg(args, "scope"); s != "" {
		rs, err := e.resolveMemoryScope(s, strArg(args, "scope_id"))
		if err != nil {
			return "", err
		}
		gcScope = rs.scope
	}

	agent := resolveAgent(args)
	requestID := strArg(args, "request_id")

	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", err
	}

	out, err := runIdempotent(ctx, db, agent, requestID, "memory.gc", func(tx *sql.Tx) (any, error) {
		n, gcErr := e.memoryGCTx(ctx, tx, gcScope)
		if gcErr != nil {
			return nil, gcErr
		}
		result := struct {
			Deleted int    `json:"deleted"`
			Scope   string `json:"scope,omitempty"`
		}{n, gcScope}
		data, mErr := json.Marshal(result)
		if mErr != nil {
			return nil, fmt.Errorf("memory: gc marshal: %w", mErr)
		}
		return string(data), nil
	})
	if err != nil {
		return "", err
	}
	return out.(string), nil
}
