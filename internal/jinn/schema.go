package jinn

// Schema is the tool definitions in OpenAI function-calling format.
const Schema = `[
  {
    "type": "function",
    "function": {
      "name": "run_shell",
      "description": "Run a bash command. Returns stdout/stderr (separated in meta as 'stdout'/'stderr' fields). Truncated to last 2000 lines or 50KB. Prefixed with [exit: N], classification, and optional [hint: ...] for recovery. Error responses include error_code.",
      "parameters": {
        "type": "object",
        "properties": {
          "command": {"type": "string", "description": "bash command to execute"},
          "timeout": {"type": "integer", "description": "max seconds (default: 30)"},
          "dry_run": {"type": "boolean", "description": "preview command without executing (default: false)"},
          "force":   {"type": "boolean", "description": "If true, execute commands even when risk classification is 'dangerous'. Default false."}
        },
        "required": ["command"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "read_file",
      "description": "Read file contents with line numbers. Up to 2000 lines per call. Use start_line/end_line for large files. All error responses include error_code and suggestion fields.",
      "parameters": {
        "type": "object",
        "properties": {
          "path": {"type": "string", "description": "file path to read"},
          "start_line": {"type": "integer", "description": "first line (1-indexed, default: 1)"},
          "end_line": {"type": "integer", "description": "last line (default: start_line+1999)"},
          "tail": {"type": "integer", "description": "Read the last N lines of the file. Takes precedence over start_line/end_line. 0 = disabled.", "default": 0},
          "line_numbers": {"type": "boolean", "description": "Include cat-n style line-number prefixes in output (default: true). Set false to receive raw file content with no numbering.", "default": true},
          "truncate": {"type": "string", "enum": ["head", "tail", "middle", "none"], "description": "Strategy when output exceeds line limit. head=keep first N (default, paginates with start_line). tail=keep last N (logs). middle=keep both ends, elide middle. none=defer to byte cap only.", "default": "head"},
          "include_checksum": {"type": "boolean", "description": "When true, response meta includes sha256 hex digest of the full file content. Zero extra I/O cost (computed during read).", "default": false}
        },
        "required": ["path"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "write_file",
      "description": "Write content to a file (creates parent dirs). Atomic via temp+rename.",
      "parameters": {
        "type": "object",
        "properties": {
          "path": {"type": "string", "description": "file path to write"},
          "content": {"type": "string", "description": "file content"},
          "dry_run": {"type": "boolean", "description": "preview changes without writing (default: false)"}
        },
        "required": ["path", "content"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "edit_file",
      "description": "Replace exact text in a file. old_text must appear exactly once. On failure, error_code indicates the cause (edit_not_found, edit_not_unique, edit_no_change, etc.). Response meta includes matchType ('exact'|'fuzzy'), fuzzyNormalized, firstChangedLine, and lastChangedLine.",
      "parameters": {
        "type": "object",
        "properties": {
          "path": {"type": "string", "description": "file path to edit"},
          "old_text": {"type": "string", "description": "exact text to find (must be unique in file)"},
          "new_text": {"type": "string", "description": "replacement text"},
          "dry_run": {"type": "boolean", "description": "preview changes without writing (default: false)"},
          "fuzzy_indent": {"type": "boolean", "description": "auto-detect indentation at match site and apply to new_text (default: false)"},
          "show_context": {"type": "integer", "description": "Number of context lines to show around the edit after applying. 0 = no context.", "default": 0}
        },
        "required": ["path", "old_text", "new_text"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "multi_edit",
      "description": "Apply multiple edits across files atomically. All edits are validated first (collect-then-report); if any fail, none are applied. Failures include per-edit error details with error_code per edit. Response meta includes matchType and line range.",
      "parameters": {
        "type": "object",
        "properties": {
          "edits": {
            "type": "array",
            "description": "list of edits to apply",
            "items": {
              "type": "object",
              "properties": {
                "path": {"type": "string", "description": "file path to edit"},
                "old_text": {"type": "string", "description": "exact text to find (must be unique)"},
                "new_text": {"type": "string", "description": "replacement text"},
                "fuzzy_indent": {"type": "boolean", "description": "Re-indent old_text to match surrounding indentation before matching", "default": false},
                "show_context": {"type": "integer", "description": "Number of context lines to show around the edit after applying", "default": 0}
              },
              "required": ["path", "old_text", "new_text"]
            }
          },
          "dry_run": {"type": "boolean", "description": "Preview all edits without writing. Returns diffs for each edit.", "default": false}
        },
        "required": ["edits"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "apply_patch",
      "description": "Apply a Codex-style patch (*** Begin Patch ... *** End Patch) to create, delete, or update files atomically. Supports add-file, delete-file, and update-file operations with hunk-based editing using @@ context markers and space/minus/plus prefixed lines. All operations are validated before any file is modified; if any operation fails, none are applied.",
      "parameters": {
        "type": "object",
        "properties": {
          "patch": {"type": "string", "description": "Codex-style patch payload. Must start with '*** Begin Patch' and end with '*** End Patch'. Supports *** Add File:, *** Delete File:, and *** Update File: operations."},
          "dry_run": {"type": "boolean", "description": "Preview changes without writing (default: false)"}
        },
        "required": ["patch"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "search_files",
      "description": "Search file contents with grep. Returns file:line:match. Default limit is 500 matches. When zero results, response includes zero_match_reason ('invalid_regex', 'path_not_found', 'path_is_empty_dir', or 'pattern_matched_nothing'). Error responses include error_code.",
      "parameters": {
        "type": "object",
        "properties": {
          "pattern": {"type": "string", "description": "grep regex pattern (or literal string when literal=true)"},
          "path": {"type": "string", "description": "directory to search (default: .)"},
          "include": {"type": "string", "description": "file glob filter, e.g. *.go"},
          "context_lines": {"type": "integer", "description": "lines of context around matches (default: 0)"},
          "case_insensitive": {"type": "boolean", "description": "case-insensitive search (default: false)"},
          "literal": {"type": "boolean", "description": "treat pattern as a fixed string rather than a regex (default: false)", "default": false},
          "max_matches": {"type": "integer", "description": "Maximum number of matches to return (default: 500). When exceeded, response includes truncated=true and total_count.", "default": 500},
          "format": {"type": "string", "description": "output format: 'text' (default), 'json' (structured results with truncation metadata), or 'filenames' (filenames with match counts)", "enum": ["text", "json", "filenames"]}
        },
        "required": ["pattern"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "stat_file",
      "description": "Get file metadata without reading contents. Returns size, line count, modification time, type, encoding (utf-8|binary), line_ending (lf|crlf|mixed), and bom (none|utf-8-bom|utf-16-le|utf-16-be) for regular files.",
      "parameters": {
        "type": "object",
        "properties": {
          "path": {"type": "string", "description": "file path to stat"}
        },
        "required": ["path"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "list_dir",
      "description": "List directory contents (pure Go, no subprocess). Hidden files excluded. Default limit is 500 entries (max 10000). Supports mtime filtering.",
      "parameters": {
        "type": "object",
        "properties": {
          "path": {"type": "string", "description": "directory to list (default: .)"},
          "depth": {"type": "integer", "description": "max depth (default: 3)"},
          "max_entries": {"type": "integer", "description": "Maximum number of entries to return (default: 500, cap: 10000). When exceeded, response includes truncated=true and total_count.", "default": 500},
          "changed_since": {"type": "number", "description": "Unix epoch seconds. Only list entries modified after this timestamp. 0 or absent = no filter.", "default": 0}
        }
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "find_files",
      "description": "Find files by glob pattern. Uses fd when available (respects .gitignore), falls back to POSIX find. Returns matching file paths relative to workdir. Default limit is 1000 results.",
      "parameters": {
        "type": "object",
        "properties": {
          "pattern": {"type": "string", "description": "Glob pattern to match files, e.g. '*.go', '**/*.json', or 'src/**/*_test.go'"},
          "path": {"type": "string", "description": "Directory to search in (default: current directory)"},
          "limit": {"type": "integer", "description": "Maximum number of results (default: 1000)", "default": 1000}
        },
        "required": ["pattern"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "list_tools",
      "description": "List available tools with their schemas and capability metadata (jinn_version, tool list, feature flags per tool).",
      "parameters": {
        "type": "object",
        "properties": {}
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "checksum_tree",
      "description": "Compute SHA-256 hashes for a file tree. Returns JSON map of {path: hash}. Supports differential mode with baseline comparison.",
      "parameters": {
        "type": "object",
        "properties": {
          "path": {"type": "string", "description": "directory to checksum (default: .)"},
          "pattern": {"type": "string", "description": "filepath glob filter, e.g. *.go"},
          "baseline": {"type": "object", "description": "Optional map of {path: sha256} from a previous run. When provided, response includes changed/added/removed arrays and only returns hashes for changed files.", "additionalProperties": {"type": "string"}}
        }
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "detect_project",
      "description": "Detect project language, framework, build tool, test command, and linter from config files.",
      "parameters": {
        "type": "object",
        "properties": {
          "path": {"type": "string", "description": "directory to analyze (default: .)"}
        }
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "memory",
      "description": "Persistent key/value memory for the agent. Save, recall, list, or forget keys across sessions. Stored at ~/.config/jinn/memory.json (or $JINN_CONFIG_DIR/memory.json).",
      "parameters": {
        "type": "object",
        "properties": {
          "action": {"type": "string", "enum": ["save", "recall", "list", "forget"], "description": "Operation to perform."},
          "key":    {"type": "string", "description": "Key name (1-128 chars, [a-zA-Z0-9_.-]). Required for save, recall, forget."},
          "value":  {"type": "string", "description": "Value to store (max 16 KiB). Required for save."}
        },
        "required": ["action"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "undo",
      "description": "Manage file mutation history. Snapshots are captured automatically before write_file, edit_file, and multi_edit operations. Use list to browse, preview to diff, restore to revert.",
      "parameters": {
        "type": "object",
        "properties": {
          "action": {"type": "string", "enum": ["list", "preview", "restore", "clear"], "description": "Operation: list=show history, preview=diff snapshot vs current, restore=revert file, clear=delete all history for this workdir."},
          "id":     {"type": "string", "description": "Snapshot ID (prefix match). Required for preview and restore."},
          "path":   {"type": "string", "description": "Reserved for future filtering. Not used currently."},
          "limit":  {"type": "integer", "description": "Max entries to return for list (default: 50)."}
        },
        "required": ["action"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "lsp_query",
      "description": "Query a language server (gopls, rust-analyzer, pylsp, typescript-language-server) for semantic info at a source location. Auto-detects the right server from file extension.",
      "parameters": {
        "type": "object",
        "properties": {
          "action":    {"type": "string", "enum": ["definition", "references", "hover", "symbols"], "description": "Query type."},
          "path":      {"type": "string", "description": "Path to source file, relative to workDir."},
          "line":      {"type": "integer", "description": "1-based line number. Required for definition/references/hover."},
          "character": {"type": "integer", "description": "1-based character offset within the line. Required for definition/references/hover."}
        },
        "required": ["action", "path"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "diff_files",
      "description": "Compare two files and return a unified diff. Uses the same diff engine as edit_file preview. Response meta includes is_identical (bool) and first_changed_line (int).",
      "parameters": {
        "type": "object",
        "properties": {
          "path_a": {"type": "string", "description": "first file path"},
          "path_b": {"type": "string", "description": "second file path"},
          "context_lines": {"type": "integer", "description": "lines of context around changes (default: 3)", "default": 3}
        },
        "required": ["path_a", "path_b"]
      }
    }
  }
]`

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
	"edit_file":     {"dry_run", "fuzzy_indent", "show_context"},
	"multi_edit":    {"overlap_detection", "show_context", "dry_run"},
	"run_shell":     {"risk_classification", "exit_classification", "dry_run", "stdout_stderr_split", "recovery_hints"},
	"search_files":  {"literal", "context_lines", "format_json", "case_insensitive", "zero_match_reason"},
	"read_file":     {"truncate_strategy", "include_checksum", "tail"},
	"write_file":    {"dry_run"},
	"stat_file":     {"encoding_detection", "line_ending_detection", "bom_detection"},
	"list_dir":      {"changed_since"},
	"checksum_tree": {"baseline_diff"},
	"diff_files":    {"context_lines"},
	"lsp_query":     {"definition", "references", "hover", "symbols"},
}

// Request is the one-shot tool invocation envelope.
type Request struct {
	Tool      string                 `json:"tool"`
	Args      map[string]interface{} `json:"args"`
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
