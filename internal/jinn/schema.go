package jinn

// Schema is the tool definitions in OpenAI function-calling format.
const Schema = `[
  {
    "type": "function",
    "function": {
      "name": "run_shell",
      "description": "Run a bash command. Returns stdout/stderr (first 200 lines), prefixed with [exit: N] and a classification field indicating whether a non-zero exit is a semantic signal (expected_nonzero) or a real failure (error).",
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
      "description": "Read file contents with line numbers. Up to 200 lines per call. Use start_line/end_line for large files. On error, a 'suggestion' field provides a one-sentence next-step hint.",
      "parameters": {
        "type": "object",
        "properties": {
          "path": {"type": "string", "description": "file path to read"},
          "start_line": {"type": "integer", "description": "first line (1-indexed, default: 1)"},
          "end_line": {"type": "integer", "description": "last line (default: start_line+199)"},
          "tail": {"type": "integer", "description": "Read the last N lines of the file. Takes precedence over start_line/end_line. 0 = disabled.", "default": 0}
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
      "description": "Replace exact text in a file. old_text must appear exactly once. On multi-match failure, the error includes line numbers of all matches (up to 10) so you can add surrounding context to disambiguate. Atomic via temp+rename.",
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
      "description": "Apply multiple edits across files atomically. All edits are validated first; if any fail, none are applied. On multi-match failure, line numbers are included in the error.",
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
          }
        },
        "required": ["edits"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "search_files",
      "description": "Search file contents with grep. Returns file:line:match. Default limit is 500 matches; set max_matches to adjust. When truncated, response includes truncated=true and total_count.",
      "parameters": {
        "type": "object",
        "properties": {
          "pattern": {"type": "string", "description": "grep regex pattern"},
          "path": {"type": "string", "description": "directory to search (default: .)"},
          "include": {"type": "string", "description": "file glob filter, e.g. *.go"},
          "context_lines": {"type": "integer", "description": "lines of context around matches (default: 0)"},
          "case_insensitive": {"type": "boolean", "description": "case-insensitive search (default: false)"},
          "max_matches": {"type": "integer", "description": "Maximum number of matches to return (default: 500). Distinct from per-line truncation. When exceeded, response includes truncated=true and total_count.", "default": 500},
          "max_results": {"type": "integer", "description": "Deprecated alias for max_matches. Use max_matches instead.", "default": 0},
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
      "description": "Get file metadata without reading contents. Returns size, line count, modification time, and type.",
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
      "description": "List directory contents. Hidden files excluded. Default limit is 500 entries (max 10000); set max_entries to adjust. When truncated, response includes truncated=true and total_count.",
      "parameters": {
        "type": "object",
        "properties": {
          "path": {"type": "string", "description": "directory to list (default: .)"},
          "depth": {"type": "integer", "description": "max depth (default: 3)"},
          "max_entries": {"type": "integer", "description": "Maximum number of entries to return (default: 500, cap: 10000). When exceeded, response includes truncated=true and total_count.", "default": 500}
        }
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "list_tools",
      "description": "List available tools and their descriptions.",
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
      "description": "Compute SHA-256 hashes for a file tree. Returns JSON map of {path: hash}.",
      "parameters": {
        "type": "object",
        "properties": {
          "path": {"type": "string", "description": "directory to checksum (default: .)"},
          "pattern": {"type": "string", "description": "filepath glob filter, e.g. *.go"}
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
  }
]`

// Request is the one-shot tool invocation envelope.
type Request struct {
	Tool string                 `json:"tool"`
	Args map[string]interface{} `json:"args"`
}

// Response is the one-shot tool result envelope.
type Response struct {
	OK             bool   `json:"ok"`
	Result         string `json:"result,omitempty"`
	Error          string `json:"error,omitempty"`
	Suggestion     string `json:"suggestion,omitempty"`
	Classification string `json:"classification,omitempty"` // exit-code class: "success", "expected_nonzero", "error", "timeout", "signal"
	Risk           string `json:"risk,omitempty"`           // pre-execution risk: "safe", "caution", "dangerous" — only set by run_shell
}
