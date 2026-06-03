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

	db, err := e.memDBConn(ctx)
	if err != nil {
		return "", err
	}

	var artifact *Artifact
	err = transact(ctx, db, func(tx *sql.Tx) error {
		if projErr := ensureProject(ctx, tx, projectID); projErr != nil {
			return projErr
		}
		a, insErr := insertArtifactTx(ctx, tx, agent, taskID, projectID, filePath, contentType)
		if insErr != nil {
			return insErr
		}
		artifact = a
		return nil
	})
	if err != nil {
		return "", err
	}
	return marshalJSON(artifact)
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
