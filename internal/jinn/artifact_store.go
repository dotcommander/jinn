package jinn

import (
	"context"
	"database/sql"
	"fmt"
)

// insertArtifactTx inserts the artifact row inside the provided transaction and
// returns the populated Artifact. The caller must have already validated task_id
// and file_path. The artifact links to task_id (+ project_id) only.
func insertArtifactTx(ctx context.Context, tx *sql.Tx, taskID, projectID, filePath, contentType string) (*Artifact, error) {
	artifactID := newID("artifact")

	var ctVal any
	if contentType != "" {
		ctVal = contentType
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO artifacts (id, task_id, project_id, file_path, content_type, created_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, artifactID, taskID, projectID, filePath, ctVal)
	if err != nil {
		return nil, fmt.Errorf("artifact: insert row: %w", err)
	}

	// Read back the inserted row so CreatedAt is authoritative.
	return getArtifactTx(ctx, tx, artifactID)
}

// getArtifactTx reads a single artifact row inside a transaction.
func getArtifactTx(ctx context.Context, tx *sql.Tx, id string) (*Artifact, error) {
	var a Artifact
	var ct sql.NullString
	var createdAt string
	err := tx.QueryRowContext(ctx, `
		SELECT id, task_id, file_path, content_type, created_at
		FROM artifacts WHERE id = ?
	`, id).Scan(&a.ID, &a.TaskID, &a.FilePath, &ct, &createdAt)
	if err != nil {
		return nil, fmt.Errorf("artifact: get: %w", err)
	}
	a.ContentType = ct.String
	a.CreatedAt, _ = parseTimestamp(createdAt)
	return &a, nil
}

// listArtifactsDB returns artifacts for a task, ordered by created_at ASC.
func listArtifactsDB(ctx context.Context, db *sql.DB, taskID string) ([]*Artifact, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, task_id, file_path, content_type, created_at
		FROM artifacts WHERE task_id = ?
		ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("artifact: list: %w", err)
	}
	defer rows.Close()

	var out []*Artifact
	for rows.Next() {
		var a Artifact
		var ct sql.NullString
		var createdAt string
		if err := rows.Scan(&a.ID, &a.TaskID, &a.FilePath, &ct, &createdAt); err != nil {
			return nil, fmt.Errorf("artifact: scan: %w", err)
		}
		a.ContentType = ct.String
		a.CreatedAt, _ = parseTimestamp(createdAt)
		out = append(out, &a)
	}
	return out, rows.Err()
}
