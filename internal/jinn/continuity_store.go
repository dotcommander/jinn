package jinn

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// Task represents a continuity task row.
type Task struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Description   string    `json:"description,omitempty"`
	Status        string    `json:"status"`
	Priority      int       `json:"priority"`
	ProjectID     string    `json:"project_id,omitempty"`
	BlockedReason string    `json:"blocked_reason,omitempty"`
	Version       int       `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Event represents a continuity event row.
type Event struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	AgentName string    `json:"agent_name"`
	ProjectID string    `json:"project_id,omitempty"`
	TaskID    string    `json:"task_id,omitempty"`
	Message   string    `json:"message"`
	Metadata  string    `json:"metadata,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// newID generates a prefixed string id: prefix_<unix_nano>_<4-byte hex>.
func newID(prefix string) string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		b = []byte{0, 0, 0, 0}
	}
	return fmt.Sprintf("%s_%d_%s", prefix, time.Now().UnixNano(), hex.EncodeToString(b))
}

// ensureProject inserts a projects row for projectID if absent. No-op when empty.
func ensureProject(ctx context.Context, tx *sql.Tx, projectID string) error {
	if projectID == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO projects (id, name) VALUES (?, ?)`,
		projectID, filepath.Base(projectID))
	if err != nil {
		return fmt.Errorf("ensureProject: %w", err)
	}
	return nil
}

// Event payload size constraints (ported from vybe store/events.go).
const (
	maxEventKind     = 128
	maxEventAgent    = 128
	maxEventMessage  = 4096
	maxEventMetadata = 16384
)

// errInvalidArgs constructs an ErrWithSuggestion for ErrCodeInvalidArgs.
func errInvalidArgs(msg, suggestion string) error {
	return &ErrWithSuggestion{Err: errors.New(msg), Suggestion: suggestion, Code: ErrCodeInvalidArgs}
}

// parseTimestamp parses a timestamp string stored by SQLite (RFC3339 or datetime).
func parseTimestamp(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05", s)
}

// transact runs fn inside a DB transaction, rolling back on error.
func transact(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
