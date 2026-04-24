package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// primaryField is the single most-informative field for each tool.
// When only this field (after noise removal) remains, show its value directly.
var primaryField = map[string]string{
	"run_shell":      "command",
	"read_file":      "path",
	"write_file":     "path",
	"edit_file":      "path",
	"multi_edit":     "path",
	"search_files":   "pattern",
	"stat_file":      "path",
	"list_dir":       "path",
	"checksum_tree":  "path",
	"detect_project": "path",
}

// filterToolArgs produces a compact display string from a tool's JSON args:
//   - strips "dry_run": false (noise when false, meaningful only when true)
//   - if a single primary field remains, returns just {value}
//   - otherwise falls through to truncated JSON
func filterToolArgs(name, argsJSON string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return previewArgs(argsJSON)
	}
	// Strip dry_run when false — it's the default, not useful to display.
	if v, ok := m["dry_run"]; ok {
		if b, ok := v.(bool); ok && !b {
			delete(m, "dry_run")
		}
	}
	// If only the primary field remains, show {value} instead of full JSON.
	if pf, ok := primaryField[name]; ok && len(m) == 1 {
		if val, ok := m[pf]; ok {
			return fmt.Sprintf("{%v}", val)
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return previewArgs(argsJSON)
	}
	return previewArgs(string(out))
}

func previewArgs(argsJSON string) string {
	s := strings.TrimSpace(argsJSON)
	s = strings.ReplaceAll(s, "\n", " ")
	return truncate(s, 120)
}
