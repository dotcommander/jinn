package jinn

import "time"

// Project is a projects-table row.
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Metadata  string    `json:"metadata,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Memory is a memory-table row as surfaced in a brief (subset of columns).
type Memory struct {
	ID        int64     `json:"id"`
	Scope     string    `json:"scope"`
	ScopeID   string    `json:"scope_id,omitempty"`
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	ValueType string    `json:"value_type"`
	Kind      string    `json:"kind"`
	Pinned    bool      `json:"pinned"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	CreatedAt time.Time `json:"created_at"`
}

// Artifact is an artifacts-table row linked to a task.
type Artifact struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	FilePath    string    `json:"file_path"`
	ContentType string    `json:"content_type,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}
