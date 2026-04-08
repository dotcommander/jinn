package jinn

// Schema is the tool definitions in OpenAI function-calling format.
const Schema = `[
  {
    "type": "function",
    "function": {
      "name": "run_shell",
      "description": "Run a bash command. Returns stdout/stderr (first 200 lines), prefixed with [exit: N].",
      "parameters": {
        "type": "object",
        "properties": {
          "command": {"type": "string", "description": "bash command to execute"},
          "timeout": {"type": "integer", "description": "max seconds (default: 30)"},
          "dry_run": {"type": "boolean", "description": "preview command without executing (default: false)"}
        },
        "required": ["command"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "read_file",
      "description": "Read file contents with line numbers. Up to 200 lines per call. Use start_line/end_line for large files.",
      "parameters": {
        "type": "object",
        "properties": {
          "path": {"type": "string", "description": "file path to read"},
          "start_line": {"type": "integer", "description": "first line (1-indexed, default: 1)"},
          "end_line": {"type": "integer", "description": "last line (default: start_line+199)"}
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
          "content": {"type": "string", "description": "file content"}
        },
        "required": ["path", "content"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "edit_file",
      "description": "Replace exact text in a file. old_text must appear exactly once. Atomic via temp+rename.",
      "parameters": {
        "type": "object",
        "properties": {
          "path": {"type": "string", "description": "file path to edit"},
          "old_text": {"type": "string", "description": "exact text to find (must be unique in file)"},
          "new_text": {"type": "string", "description": "replacement text"}
        },
        "required": ["path", "old_text", "new_text"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "multi_edit",
      "description": "Apply multiple edits across files atomically. All edits are validated first; if any fail, none are applied.",
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
                "new_text": {"type": "string", "description": "replacement text"}
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
      "description": "Search file contents with grep. Returns file:line:match. Max 100 results.",
      "parameters": {
        "type": "object",
        "properties": {
          "pattern": {"type": "string", "description": "grep regex pattern"},
          "path": {"type": "string", "description": "directory to search (default: .)"},
          "include": {"type": "string", "description": "file glob filter, e.g. *.go"},
          "context_lines": {"type": "integer", "description": "lines of context around matches (default: 0)"},
          "case_insensitive": {"type": "boolean", "description": "case-insensitive search (default: false)"}
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
      "description": "List directory contents. Hidden files excluded.",
      "parameters": {
        "type": "object",
        "properties": {
          "path": {"type": "string", "description": "directory to list (default: .)"},
          "depth": {"type": "integer", "description": "max depth (default: 3)"}
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
  }
]`

// Request is the one-shot tool invocation envelope.
type Request struct {
	Tool string                 `json:"tool"`
	Args map[string]interface{} `json:"args"`
}

// Response is the one-shot tool result envelope.
type Response struct {
	OK     bool   `json:"ok"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}
