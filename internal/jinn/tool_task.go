package jinn

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
)

// taskTool dispatches task sub-actions: create | begin | set_status | get | list.
func (e *Engine) taskTool(ctx context.Context, args map[string]interface{}) (string, error) {
	action := strArg(args, "action")
	switch action {
	case "create":
		return e.taskCreate(ctx, args)
	case "begin":
		return e.taskBegin(ctx, args)
	case "set_status":
		return e.taskSetStatus(ctx, args)
	case "get":
		return e.taskGet(ctx, args)
	case "list":
		return e.taskList(ctx, args)
	default:
		return "", errInvalidArgs(
			fmt.Sprintf("unknown task action %q", action),
			"use create|begin|set_status|get|list",
		)
	}
}

func (e *Engine) taskCreate(ctx context.Context, args map[string]interface{}) (string, error) {
	title := strArg(args, "title")
	if title == "" {
		return "", errInvalidArgs("title is required", "provide a non-empty title")
	}
	description := strArg(args, "description")
	priority := intArg(args, "priority", 0)
	// intArg returns def only when <= 0, but 0 is a valid default priority.
	// Re-read without the >0 guard:
	if v, ok := args["priority"].(float64); ok {
		priority = int(v)
	}
	agent := resolveAgent(args)
	projectID := e.resolveProjectID(args)
	requestID := strArg(args, "request_id")

	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", err
	}

	result, err := runIdempotent(ctx, db, agent, requestID, "task.create", func(tx *sql.Tx) (any, error) {
		if projErr := ensureProject(ctx, tx, projectID); projErr != nil {
			return nil, projErr
		}
		t, insertErr := insertTaskTx(ctx, tx, title, description, projectID, priority)
		if insertErr != nil {
			return nil, insertErr
		}
		_, evtErr := insertEventTx(ctx, tx, "task_created", agent, projectID, t.ID,
			fmt.Sprintf("Task created: %s", title), "")
		if evtErr != nil {
			return nil, evtErr
		}
		return t, nil
	})
	if err != nil {
		return "", err
	}
	return result.(string), nil
}

func (e *Engine) taskBegin(ctx context.Context, args map[string]interface{}) (string, error) {
	taskID := strArg(args, "task_id")
	if taskID == "" {
		return "", errInvalidArgs("task_id is required", "provide the task id to begin")
	}
	agent := resolveAgent(args)
	requestID := strArg(args, "request_id")

	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", err
	}

	result, err := runIdempotent(ctx, db, agent, requestID, "task.begin", func(tx *sql.Tx) (any, error) {
		return updateTaskStatusTx(ctx, tx, taskID, "in_progress", agent, "task_started")
	})
	if err != nil {
		return "", err
	}
	return result.(string), nil
}

func (e *Engine) taskSetStatus(ctx context.Context, args map[string]interface{}) (string, error) {
	taskID := strArg(args, "task_id")
	if taskID == "" {
		return "", errInvalidArgs("task_id is required", "provide the task id")
	}
	status := strArg(args, "status")
	switch status {
	case "pending", "in_progress", "completed", "blocked":
	default:
		return "", errInvalidArgs(
			fmt.Sprintf("invalid status %q", status),
			"use pending|in_progress|completed|blocked",
		)
	}
	blockedReason := strArg(args, "blocked_reason")
	agent := resolveAgent(args)
	requestID := strArg(args, "request_id")

	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", err
	}

	result, err := runIdempotent(ctx, db, agent, requestID, "task.set_status", func(tx *sql.Tx) (any, error) {
		t, txErr := updateTaskStatusTx(ctx, tx, taskID, status, agent, "task_status")
		if txErr != nil {
			return nil, txErr
		}
		if status == "blocked" && blockedReason != "" {
			if brErr := setBlockedReasonTx(ctx, tx, taskID, blockedReason); brErr != nil {
				return nil, brErr
			}
			t.BlockedReason = blockedReason
		}
		return t, nil
	})
	if err != nil {
		return "", err
	}
	return result.(string), nil
}

func (e *Engine) taskGet(ctx context.Context, args map[string]interface{}) (string, error) {
	taskID := strArg(args, "task_id")
	if taskID == "" {
		return "", errInvalidArgs("task_id is required", "provide the task id to get")
	}
	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", err
	}
	task, err := getTaskDB(ctx, db, taskID)
	if err != nil {
		return "", err
	}
	return marshalJSON(task)
}

func (e *Engine) taskList(ctx context.Context, args map[string]interface{}) (string, error) {
	statusFilter := strArg(args, "status")
	projectFilter := strArg(args, "project_id")
	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", err
	}
	tasks, err := listTasksDB(ctx, db, statusFilter, projectFilter)
	if err != nil {
		return "", err
	}
	if tasks == nil {
		tasks = []*Task{}
	}
	return marshalJSON(tasks)
}

// resolveAgent returns the agent identity: args["agent"] → JINN_CLIENT → "agent".
func resolveAgent(args map[string]interface{}) string {
	if v := strArg(args, "agent"); v != "" {
		return v
	}
	if v := os.Getenv("JINN_CLIENT"); v != "" {
		return v
	}
	return "agent"
}

// resolveProjectID returns the project id: args["project_id"] → e.currentProjectID().
func (e *Engine) resolveProjectID(args map[string]interface{}) string {
	if v := strArg(args, "project_id"); v != "" {
		return v
	}
	return e.currentProjectID()
}

// marshalJSON serializes v to a JSON string.
func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal result: %w", err)
	}
	return string(b), nil
}
