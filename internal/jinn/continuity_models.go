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
	EventID     int64     `json:"event_id"`
	FilePath    string    `json:"file_path"`
	ContentType string    `json:"content_type,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// TaskStatusCounts breaks down task counts by status.
type TaskStatusCounts struct {
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
	Blocked    int `json:"blocked"`
}

// PipelineTask is a lightweight pending-task reference for discovery context.
type PipelineTask struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Priority int    `json:"priority"`
}

// AgentCursorFocus holds the persisted cursor + focus pointers for an agent.
type AgentCursorFocus struct {
	Cursor    int64
	TaskID    string
	ProjectID string
	Exists    bool // false when no agent_state row exists yet (peek empty-state)
}

// BriefPacket is the resume/peek output (design-doc shape).
type BriefPacket struct {
	BriefVersion   string            `json:"brief_version"`
	FocusRule      string            `json:"focus_rule"`
	Cursor         CursorWindow      `json:"cursor"`
	Task           *Task             `json:"task"`
	Project        *Project          `json:"project"`
	RelevantMemory []*Memory         `json:"relevant_memory"`
	RecentEvents   []*Event          `json:"recent_events"`
	Artifacts      []*Artifact       `json:"artifacts"`
	Counts         *TaskStatusCounts `json:"counts"`
	Pipeline       []PipelineTask    `json:"pipeline"`
	Deltas         []*Event          `json:"deltas"`
	ApproxTokens   int               `json:"approx_tokens"`
}

// CursorWindow reports the cursor before/after this resume.
type CursorWindow struct {
	Old int64 `json:"old"`
	New int64 `json:"new"`
}
