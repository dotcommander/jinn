package jinn

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// eventTool dispatches event sub-actions: append | list.
func (e *Engine) eventTool(ctx context.Context, args map[string]interface{}) (string, error) {
	action := strArg(args, "action")
	switch action {
	case "append":
		return e.eventAppend(ctx, args)
	case "list":
		return e.eventList(ctx, args)
	default:
		return "", errInvalidArgs(
			fmt.Sprintf("unknown event action %q", action),
			"use append|list",
		)
	}
}

func (e *Engine) eventAppend(ctx context.Context, args map[string]interface{}) (string, error) {
	kind := strArg(args, "kind")
	message := strArg(args, "message")
	agent := resolveAgent(args)
	taskID := strArg(args, "task_id")
	projectID := e.resolveProjectID(args)

	// metadata: accept a JSON object arg or a JSON string; store as TEXT.
	metadata, err := resolveMetadata(args)
	if err != nil {
		return "", err
	}

	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", err
	}

	var eventID int64
	txErr := transact(ctx, db, func(tx *sql.Tx) error {
		if projErr := ensureProject(ctx, tx, projectID); projErr != nil {
			return projErr
		}
		id, insErr := insertEventTx(ctx, tx, kind, agent, projectID, taskID, message, metadata)
		if insErr != nil {
			return insErr
		}
		eventID = id
		return nil
	})
	if txErr != nil {
		return "", txErr
	}

	result := map[string]any{
		"id":         eventID,
		"kind":       kind,
		"agent_name": agent,
		"project_id": projectID,
		"task_id":    taskID,
		"message":    message,
	}
	if metadata != "" {
		result["metadata"] = metadata
	}
	b, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("marshal event result: %w", err)
	}
	return string(b), nil
}

func (e *Engine) eventList(ctx context.Context, args map[string]interface{}) (string, error) {
	taskFilter := strArg(args, "task_id")
	projectFilter := strArg(args, "project_id")
	kindFilter := strArg(args, "kind")
	limit := intArg(args, "limit", 20)
	// intArg returns def when <=0; for limit we also need to handle explicit 0 → default.
	if v, ok := args["limit"].(float64); ok && int(v) > 0 {
		limit = int(v)
	}
	var sinceID int64
	if v, ok := args["since_id"].(float64); ok {
		sinceID = int64(v)
	}

	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", err
	}
	events, err := listEventsDB(ctx, db, taskFilter, projectFilter, kindFilter, limit, sinceID)
	if err != nil {
		return "", err
	}
	if events == nil {
		events = []*Event{}
	}
	return marshalJSON(events)
}

// resolveMetadata normalises the metadata arg: accepts a map (JSON object) or a
// string (must be valid JSON when non-empty). Returns the canonical JSON string
// to store, or "" when absent.
func resolveMetadata(args map[string]interface{}) (string, error) {
	raw, ok := args["metadata"]
	if !ok || raw == nil {
		return "", nil
	}
	switch v := raw.(type) {
	case string:
		if v == "" {
			return "", nil
		}
		// Validate that it is already valid JSON.
		if !json.Valid([]byte(v)) {
			return "", errInvalidArgs("metadata string must be valid JSON", "pass a JSON object string")
		}
		return v, nil
	case map[string]interface{}:
		b, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("marshal metadata: %w", err)
		}
		return string(b), nil
	default:
		return "", errInvalidArgs("metadata must be a JSON object or JSON string", "pass an object or a JSON-encoded string")
	}
}
