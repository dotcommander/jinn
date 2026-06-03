package jinn

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	maxPushMemories  = 50
	maxPushArtifacts = 50
)

// pushTool executes an atomic multi-op batch: event, artifacts, memories, task_status.
// All operations share one transaction — any failure rolls back the entire batch.
func (e *Engine) pushTool(ctx context.Context, args map[string]interface{}) (string, error) {
	agent := resolveAgent(args)
	taskID := strArg(args, "task_id")
	projectID := e.resolveProjectID(args)

	// Parse optional event.
	var pushEvent *pushEventInput
	if raw, ok := args["event"]; ok && raw != nil {
		m, ok := raw.(map[string]interface{})
		if !ok {
			return "", errInvalidArgs("event must be an object", "pass event as a JSON object")
		}
		pushEvent = &pushEventInput{
			Kind:    strArg(m, "kind"),
			Message: strArg(m, "message"),
		}
		if meta, err := resolveMetadata(m); err != nil {
			return "", err
		} else {
			pushEvent.Metadata = meta
		}
	}

	// Parse optional artifacts array.
	var artifacts []pushArtifactInput
	if raw, ok := args["artifacts"]; ok && raw != nil {
		arr, ok := raw.([]interface{})
		if !ok {
			return "", errInvalidArgs("artifacts must be an array", "pass artifacts as a JSON array")
		}
		for i, item := range arr {
			m, ok := item.(map[string]interface{})
			if !ok {
				return "", errInvalidArgs(fmt.Sprintf("artifacts[%d] must be an object", i), "each artifact must be a JSON object")
			}
			fp := strArg(m, "file_path")
			if fp == "" {
				return "", errInvalidArgs(fmt.Sprintf("artifacts[%d].file_path is required", i), "provide file_path for each artifact")
			}
			artifacts = append(artifacts, pushArtifactInput{
				FilePath:    fp,
				ContentType: strArg(m, "content_type"),
			})
		}
	}

	// Parse optional memories array.
	var memories []pushMemoryInput
	if raw, ok := args["memories"]; ok && raw != nil {
		arr, ok := raw.([]interface{})
		if !ok {
			return "", errInvalidArgs("memories must be an array", "pass memories as a JSON array")
		}
		for i, item := range arr {
			m, ok := item.(map[string]interface{})
			if !ok {
				return "", errInvalidArgs(fmt.Sprintf("memories[%d] must be an object", i), "each memory must be a JSON object")
			}
			key := strArg(m, "key")
			if key == "" {
				return "", errInvalidArgs(fmt.Sprintf("memories[%d].key is required", i), "provide key for each memory")
			}
			kind := strArg(m, "kind")
			if kind == "" {
				kind = "fact"
			}
			if err := validateKind(kind); err != nil {
				return "", fmt.Errorf("memories[%d]: %w", i, err)
			}
			expiresAt, err := parseExpiresIn(strArg(m, "expires_in"))
			if err != nil {
				return "", fmt.Errorf("memories[%d]: %w", i, err)
			}
			memories = append(memories, pushMemoryInput{
				Key:       key,
				Value:     strArg(m, "value"),
				Scope:     strArg(m, "scope"),
				ScopeID:   strArg(m, "scope_id"),
				Kind:      kind,
				Pin:       boolArg(m, "pin"),
				ExpiresAt: expiresAt,
			})
		}
	}

	// Parse optional task_status.
	var taskStatus *pushTaskStatusInput
	if raw, ok := args["task_status"]; ok && raw != nil {
		m, ok := raw.(map[string]interface{})
		if !ok {
			return "", errInvalidArgs("task_status must be an object", "pass task_status as a JSON object")
		}
		taskStatus = &pushTaskStatusInput{
			Status:        strArg(m, "status"),
			BlockedReason: strArg(m, "blocked_reason"),
		}
	}

	// Validation.
	if pushEvent == nil && len(memories) == 0 && len(artifacts) == 0 && taskStatus == nil {
		return "", errInvalidArgs("push requires at least one operation (event, memories, artifacts, or task_status)",
			"provide at least one field: event, memories, artifacts, or task_status")
	}
	if len(artifacts) > 0 && taskID == "" {
		return "", errInvalidArgs("task_id is required when artifacts are provided", "provide task_id")
	}
	if taskStatus != nil && taskID == "" {
		return "", errInvalidArgs("task_id is required when task_status is provided", "provide task_id")
	}
	if len(memories) > maxPushMemories {
		return "", errInvalidArgs(fmt.Sprintf("push exceeds maximum memories (%d > %d)", len(memories), maxPushMemories), "reduce memories batch size")
	}
	if len(artifacts) > maxPushArtifacts {
		return "", errInvalidArgs(fmt.Sprintf("push exceeds maximum artifacts (%d > %d)", len(artifacts), maxPushArtifacts), "reduce artifacts batch size")
	}

	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", err
	}

	type pushResult struct {
		EventID     int64    `json:"event_id,omitempty"`
		MemoryCount int      `json:"memory_count"`
		ArtifactIDs []string `json:"artifact_ids"`
		Task        *Task    `json:"task"`
	}

	var result pushResult
	result.ArtifactIDs = []string{}

	err = transact(ctx, db, func(tx *sql.Tx) error {
		if projErr := ensureProject(ctx, tx, projectID); projErr != nil {
			return projErr
		}

		// Step 1: event (or synthesize one when artifacts need an event_id).
		var eventID int64
		if pushEvent != nil {
			id, evErr := insertEventTx(ctx, tx, pushEvent.Kind, agent, projectID, taskID, pushEvent.Message, pushEvent.Metadata)
			if evErr != nil {
				return fmt.Errorf("push: insert event: %w", evErr)
			}
			eventID = id
			result.EventID = id
		} else if len(artifacts) > 0 {
			// Synthesize a minimal push event so artifacts.event_id is never NULL.
			id, evErr := insertEventTx(ctx, tx, "push", agent, projectID, taskID, "artifact batch", "")
			if evErr != nil {
				return fmt.Errorf("push: synthesize event: %w", evErr)
			}
			eventID = id
			result.EventID = id
		}

		// Step 2: artifacts (all linked to eventID from step 1).
		for _, art := range artifacts {
			artifactID := newID("artifact")
			var ctVal any
			if art.ContentType != "" {
				ctVal = art.ContentType
			}
			_, insErr := tx.ExecContext(ctx, `
				INSERT INTO artifacts (id, task_id, project_id, event_id, file_path, content_type, created_at)
				VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			`, artifactID, taskID, projectID, eventID, art.FilePath, ctVal)
			if insErr != nil {
				return fmt.Errorf("push: insert artifact %q: %w", art.FilePath, insErr)
			}
			result.ArtifactIDs = append(result.ArtifactIDs, artifactID)
		}

		// Step 3: memories via the shared memoryUpsertTx.
		for i, mem := range memories {
			rs, rsErr := e.resolveMemoryScope(mem.Scope, mem.ScopeID)
			if rsErr != nil {
				return fmt.Errorf("push: memories[%d] scope: %w", i, rsErr)
			}
			if err := validateKey(mem.Key); err != nil {
				return fmt.Errorf("push: memories[%d]: %w", i, err)
			}
			if uErr := memoryUpsertTx(ctx, tx, rs.scope, rs.scopeID, mem.Key, mem.Value, mem.Kind, mem.Pin, mem.ExpiresAt); uErr != nil {
				return fmt.Errorf("push: memories[%d] upsert: %w", i, uErr)
			}
			result.MemoryCount++
		}

		// Step 4: task status change (emits its own event inside the same tx).
		if taskStatus != nil {
			t, tsErr := updateTaskStatusTx(ctx, tx, taskID, taskStatus.Status, agent, "task_status")
			if tsErr != nil {
				return fmt.Errorf("push: task_status: %w", tsErr)
			}
			if taskStatus.Status == "blocked" && taskStatus.BlockedReason != "" {
				if brErr := setBlockedReasonTx(ctx, tx, taskID, taskStatus.BlockedReason); brErr != nil {
					return fmt.Errorf("push: set blocked_reason: %w", brErr)
				}
				t.BlockedReason = taskStatus.BlockedReason
			}
			result.Task = t
		}

		return nil
	})
	if err != nil {
		return "", err
	}

	return marshalJSON(result)
}

// pushEventInput holds parsed event fields from the push args.
type pushEventInput struct {
	Kind     string
	Message  string
	Metadata string
}

// pushArtifactInput holds parsed artifact fields from the push args.
type pushArtifactInput struct {
	FilePath    string
	ContentType string
}

// pushMemoryInput holds parsed memory fields from the push args.
type pushMemoryInput struct {
	Key       string
	Value     string
	Scope     string
	ScopeID   string
	Kind      string
	Pin       bool
	ExpiresAt *time.Time
}

// pushTaskStatusInput holds parsed task_status fields from the push args.
type pushTaskStatusInput struct {
	Status        string
	BlockedReason string
}
