package jinn

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
)

// Schema is the tool definitions in OpenAI function-calling format.
//
//go:embed schema.json
var Schema string

// ToolCapabilities describes the features available in this jinn version.
// Returned by the list_tools tool so callers can adapt behavior to
// what the current build supports (e.g. dry_run, fuzzy_indent, etc.).
type ToolCapabilities struct {
	JinnVersion string              `json:"jinn_version"`
	Tools       []string            `json:"tools"`
	Features    map[string][]string `json:"features"`
	Schema      any                 `json:"schema,omitempty"`
}

// Request is the one-shot tool invocation envelope.
type Request struct {
	Tool      string                 `json:"tool"`
	Args      map[string]interface{} `json:"args"`
	Client    string                 `json:"client,omitempty"`
	Compress  bool                   `json:"compress,omitempty"`
	RequestID string                 `json:"request_id,omitempty"`
}

// ContentBlock represents a typed piece of content in a tool response (text or image).
type ContentBlock struct {
	Type     string `json:"type"`               // "text" or "image"
	Text     string `json:"text,omitempty"`     // for type="text"
	Data     string `json:"data,omitempty"`     // base64-encoded, for type="image"
	MimeType string `json:"mimeType,omitempty"` // e.g. "image/png", for type="image"
}

// UnmarshalJSON strictly decodes the request envelope. Compatibility coercion
// is intentionally rejected at this trust boundary.
func (r *Request) UnmarshalJSON(data []byte) error {
	type aliasRequest struct {
		Tool      string          `json:"tool"`
		Args      json.RawMessage `json:"args"`
		Client    string          `json:"client,omitempty"`
		Compress  bool            `json:"compress,omitempty"`
		RequestID string          `json:"request_id,omitempty"`
	}
	var a aliasRequest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return err
	}
	if strings.TrimSpace(a.Tool) == "" {
		return errors.New("tool is required")
	}
	r.Tool = a.Tool
	r.Client = a.Client
	r.Compress = a.Compress
	r.RequestID = a.RequestID

	if len(a.Args) == 0 {
		r.Args = map[string]interface{}{}
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(a.Args, &m); err != nil || m == nil {
		return errors.New("args must be a JSON object")
	}
	r.Args = m
	return nil
}

// Response is the one-shot tool result envelope.
type Response struct {
	OK             bool           `json:"ok"`
	Result         string         `json:"result,omitempty"`  // legacy text result (backwards compat)
	Content        []ContentBlock `json:"content,omitempty"` // structured content blocks (images, etc.)
	Meta           map[string]any `json:"meta,omitempty"`    // structured metadata (truncation, etc.)
	Error          string         `json:"error,omitempty"`
	Suggestion     string         `json:"suggestion,omitempty"`
	Classification string         `json:"classification,omitempty"` // exit-code class: "success", "expected_nonzero", "error", "timeout", "signal"
	Risk           string         `json:"risk,omitempty"`           // pre-execution risk: "safe", "caution", "dangerous" — only set by run_shell
	ErrorCode      string         `json:"error_code,omitempty"`
	RequestID      string         `json:"request_id,omitempty"`
}
