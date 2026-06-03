package jinn

import (
	"context"
	"database/sql"
	"fmt"
	"time"
	"unicode/utf8"
)

// buildBriefTx assembles a BriefPacket for the resolved focus inside tx.
// asOf is the expiry cutoff for relevant_memory (pass time.Now()).
func buildBriefTx(ctx context.Context, tx *sql.Tx, focusTaskID, focusProjectID string, asOf time.Time) (*BriefPacket, error) {
	b := &BriefPacket{
		BriefVersion:   "v1",
		RelevantMemory: []*Memory{},
		RecentEvents:   []*Event{},
		Artifacts:      []*Artifact{},
		Pipeline:       []PipelineTask{},
	}

	if focusProjectID != "" {
		if p, err := getProjectTx(ctx, tx, focusProjectID); err == nil {
			b.Project = p
		}
	}

	if focusTaskID != "" {
		t, err := getTaskTx(ctx, tx, focusTaskID)
		if err != nil {
			return nil, fmt.Errorf("get focus task: %w", err)
		}
		b.Task = t

		ev, err := fetchRecentEventsTx(ctx, tx, focusTaskID)
		if err != nil {
			return nil, err
		}
		b.RecentEvents = ev

		art, err := fetchArtifactsTx(ctx, tx, focusTaskID)
		if err != nil {
			return nil, err
		}
		b.Artifacts = art
	}

	mem, err := fetchRelevantMemoryTx(ctx, tx, focusTaskID, focusProjectID, asOf)
	if err != nil {
		return nil, err
	}
	b.RelevantMemory = mem

	counts, err := taskStatusCountsTx(ctx, tx, focusProjectID)
	if err != nil {
		return nil, err
	}
	b.Counts = counts

	pipe, err := pipelineTasksTx(ctx, tx, focusTaskID, focusProjectID, 5)
	if err != nil {
		return nil, err
	}
	b.Pipeline = pipe

	return b, nil
}

func getProjectTx(ctx context.Context, tx *sql.Tx, projectID string) (*Project, error) {
	var p Project
	var meta sql.NullString
	var createdAt string
	err := tx.QueryRowContext(ctx, `
		SELECT id, name, metadata, created_at FROM projects WHERE id = ?
	`, projectID).Scan(&p.ID, &p.Name, &meta, &createdAt)
	if err != nil {
		return nil, err
	}
	p.Metadata = meta.String
	p.CreatedAt, _ = parseTimestamp(createdAt)
	return &p, nil
}

// fetchRelevantMemoryTx returns brief memory: global, the focus task scope, and
// the focus project scope (or all project rows when focusProjectID==""). Agent-
// scoped rows are EXCLUDED. Expired (non-pinned) rows excluded. pinned first.
func fetchRelevantMemoryTx(ctx context.Context, tx *sql.Tx, taskID, projectID string, asOf time.Time) ([]*Memory, error) {
	asOfStr := asOf.UTC().Format("2006-01-02 15:04:05")
	var query string
	var args []any
	if projectID != "" {
		query = `
			SELECT id, scope, scope_id, key, value, value_type, kind, pinned, expires_at, updated_at, created_at
			FROM memory
			WHERE (scope = 'global'
			       OR (scope = 'task' AND scope_id = ?)
			       OR (scope = 'project' AND scope_id = ?))
			  AND (pinned = 1 OR expires_at IS NULL OR expires_at > ?)
			ORDER BY pinned DESC, updated_at DESC
			LIMIT 50`
		args = []any{taskID, projectID, asOfStr}
	} else {
		query = `
			SELECT id, scope, scope_id, key, value, value_type, kind, pinned, expires_at, updated_at, created_at
			FROM memory
			WHERE (scope = 'global'
			       OR (scope = 'task' AND scope_id = ?)
			       OR scope = 'project')
			  AND (pinned = 1 OR expires_at IS NULL OR expires_at > ?)
			ORDER BY pinned DESC, updated_at DESC
			LIMIT 50`
		args = []any{taskID, asOfStr}
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query memory: %w", err)
	}
	defer rows.Close()
	out := make([]*Memory, 0, 50)
	for rows.Next() {
		var m Memory
		var scopeID, expiresAt sql.NullString
		var pinned int
		var updatedAt, createdAt string
		if err := rows.Scan(&m.ID, &m.Scope, &scopeID, &m.Key, &m.Value, &m.ValueType,
			&m.Kind, &pinned, &expiresAt, &updatedAt, &createdAt); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		m.ScopeID = scopeID.String
		m.Pinned = pinned == 1
		if expiresAt.Valid {
			m.ExpiresAt, _ = parseTimestamp(expiresAt.String)
		}
		m.UpdatedAt, _ = parseTimestamp(updatedAt)
		m.CreatedAt, _ = parseTimestamp(createdAt)
		out = append(out, &m)
	}
	return out, rows.Err()
}

// fetchRecentEventsTx returns the last 20 events for a task, ascending by id.
func fetchRecentEventsTx(ctx context.Context, tx *sql.Tx, taskID string) ([]*Event, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
		FROM events WHERE task_id = ? ORDER BY id DESC LIMIT 20
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query recent events: %w", err)
	}
	defer rows.Close()
	desc, err := scanEventRowsTx(rows)
	if err != nil {
		return nil, err
	}
	// reverse to ascending
	for i, j := 0, len(desc)-1; i < j; i, j = i+1, j-1 {
		desc[i], desc[j] = desc[j], desc[i]
	}
	return desc, nil
}

func fetchArtifactsTx(ctx context.Context, tx *sql.Tx, taskID string) ([]*Artifact, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, task_id, event_id, file_path, content_type, created_at
		FROM artifacts WHERE task_id = ? ORDER BY created_at DESC LIMIT 100
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query artifacts: %w", err)
	}
	defer rows.Close()
	out := make([]*Artifact, 0)
	for rows.Next() {
		var a Artifact
		var contentType sql.NullString
		var createdAt string
		if err := rows.Scan(&a.ID, &a.TaskID, &a.EventID, &a.FilePath, &contentType, &createdAt); err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		a.ContentType = contentType.String
		a.CreatedAt, _ = parseTimestamp(createdAt)
		out = append(out, &a)
	}
	return out, rows.Err()
}

func taskStatusCountsTx(ctx context.Context, tx *sql.Tx, projectID string) (*TaskStatusCounts, error) {
	c := &TaskStatusCounts{}
	q := `
		SELECT
			COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'in_progress' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'blocked' THEN 1 ELSE 0 END), 0)
		FROM tasks`
	var args []any
	if projectID != "" {
		q += " WHERE project_id = ?"
		args = append(args, projectID)
	}
	if err := tx.QueryRowContext(ctx, q, args...).Scan(&c.Pending, &c.InProgress, &c.Completed, &c.Blocked); err != nil {
		return nil, fmt.Errorf("task status counts: %w", err)
	}
	return c, nil
}

func pipelineTasksTx(ctx context.Context, tx *sql.Tx, excludeTaskID, projectID string, limit int) ([]PipelineTask, error) {
	if limit <= 0 {
		limit = 5
	}
	q := `SELECT id, title, priority FROM tasks WHERE status = 'pending' AND id != ?`
	args := []any{excludeTaskID}
	if projectID != "" {
		q += " AND project_id = ?"
		args = append(args, projectID)
	}
	q += " ORDER BY priority DESC, created_at ASC LIMIT ?"
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query pipeline: %w", err)
	}
	defer rows.Close()
	out := make([]PipelineTask, 0, limit)
	for rows.Next() {
		var p PipelineTask
		if err := rows.Scan(&p.ID, &p.Title, &p.Priority); err != nil {
			return nil, fmt.Errorf("scan pipeline: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// scanEventRowsTx scans the 8-column event row shape (tx variant).
func scanEventRowsTx(rows *sql.Rows) ([]*Event, error) {
	var out []*Event
	for rows.Next() {
		var ev Event
		var projectID, taskID, metadata sql.NullString
		var createdAt string
		if err := rows.Scan(&ev.ID, &ev.Kind, &ev.AgentName, &projectID, &taskID,
			&ev.Message, &metadata, &createdAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		ev.ProjectID = projectID.String
		ev.TaskID = taskID.String
		ev.Metadata = metadata.String
		ev.CreatedAt, _ = parseTimestamp(createdAt)
		out = append(out, &ev)
	}
	return out, rows.Err()
}

// fetchDeltasTx returns events with id > cursor, scoped to projectID when set,
// ascending, capped by limit. Returns the events and the max id seen.
func fetchDeltasTx(ctx context.Context, tx *sql.Tx, cursor int64, projectID string, limit int) ([]*Event, int64, error) {
	q := `
		SELECT id, kind, agent_name, project_id, task_id, message, metadata, created_at
		FROM events WHERE id > ?`
	args := []any{cursor}
	if projectID != "" {
		q += " AND (project_id = ? OR project_id = '')"
		args = append(args, projectID)
	}
	q += " ORDER BY id ASC LIMIT ?"
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("query deltas: %w", err)
	}
	defer rows.Close()
	evs, err := scanEventRowsTx(rows)
	if err != nil {
		return nil, 0, err
	}
	maxID := cursor
	for _, e := range evs {
		if e.ID > maxID {
			maxID = e.ID
		}
	}
	if evs == nil {
		evs = []*Event{}
	}
	return evs, maxID, nil
}

// approxTokens estimates token count as utf8 rune count / 4 over the serialized packet.
func approxTokens(serialized string) int {
	return utf8.RuneCountInString(serialized) / 4
}
