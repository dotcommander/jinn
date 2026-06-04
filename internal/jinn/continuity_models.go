package jinn

import "time"

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
