package jinn

import (
	"context"
	"database/sql"
	"fmt"
)

// artifactTool dispatches artifact sub-actions: add | list.
func (e *Engine) artifactTool(ctx context.Context, args map[string]interface{}) (string, error) {
	action := strArg(args, "action")
	switch action {
	case "add":
		return e.artifactAdd(ctx, args)
	case "list":
		return e.artifactList(ctx, args)
	default:
		return "", errInvalidArgs(
			fmt.Sprintf("unknown artifact action %q", action),
			"use add|list",
		)
	}
}

func (e *Engine) artifactAdd(ctx context.Context, args map[string]interface{}) (string, error) {
	taskID := strArg(args, "task_id")
	if taskID == "" {
		return "", errInvalidArgs("task_id is required for artifact add", "provide the task id")
	}
	filePath := strArg(args, "file_path")
	if filePath == "" {
		return "", errInvalidArgs("file_path is required for artifact add", "provide the file path")
	}
	contentType := strArg(args, "content_type")
	agent := resolveAgent(args)
	projectID := e.resolveProjectID(args)
	requestID := strArg(args, "request_id")

	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", err
	}

	result, err := runIdempotent(ctx, db, agent, requestID, "artifact.add", func(tx *sql.Tx) (any, error) {
		if projErr := ensureProject(ctx, tx, projectID); projErr != nil {
			return nil, projErr
		}
		a, insErr := insertArtifactTx(ctx, tx, agent, taskID, projectID, filePath, contentType, 0)
		if insErr != nil {
			return nil, insErr
		}
		return marshalJSON(a)
	})
	if err != nil {
		return "", err
	}
	return result.(string), nil
}

func (e *Engine) artifactList(ctx context.Context, args map[string]interface{}) (string, error) {
	taskID := strArg(args, "task_id")
	if taskID == "" {
		return "", errInvalidArgs("task_id is required for artifact list", "provide the task id")
	}
	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", err
	}
	artifacts, err := listArtifactsDB(ctx, db, taskID)
	if err != nil {
		return "", err
	}
	if artifacts == nil {
		artifacts = []*Artifact{}
	}
	return marshalJSON(artifacts)
}
