package jinn

import _ "embed"

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
}

// toolFeatures lists the optional feature flags each tool supports.
// Callers should check for a feature key in this map rather than
// hard-coding support assumptions.
var toolFeatures = map[string][]string{
	"edit_file":      {"dry_run", "fuzzy_indent", "show_context"},
	"multi_edit":     {"overlap_detection", "show_context", "dry_run"},
	"run_shell":      {"risk_classification", "exit_classification", "dry_run", "stdout_stderr_split", "recovery_hints", "compress_output"},
	"search_files":   {"literal", "context_lines", "format_json", "case_insensitive", "zero_match_reason"},
	"read_file":      {"truncate_strategy", "include_checksum", "tail"},
	"multi_read":     {"per_file_windowing", "partial_success"},
	"write_file":     {"dry_run"},
	"stat_file":      {"encoding_detection", "line_ending_detection", "bom_detection"},
	"list_dir":       {"changed_since"},
	"diff_files":     {"context_lines"},
	"search_replace": {"regex", "capture_groups", "multi_file", "glob_patterns", "replace_all", "dry_run", "case_insensitive", "multiline"},
	"lsp_query":      {"definition", "references", "hover", "symbols", "diagnostics", "rename", "symbol_column", "context_lines"},
	"task":           {"create", "begin", "set_status", "get", "list"},
	"event":          {"append", "list"},
	"resume":         {"peek", "focus_selection", "brief"},
	"artifact":       {"add", "list"},
	"push":           {"atomic_batch", "event", "memories", "artifacts", "task_status"},
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
