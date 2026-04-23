package jinn

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const maxFileSize = 50 << 20 // 50 MB

func (e *Engine) readFile(args map[string]interface{}) (string, error) {
	path, _ := args["path"].(string)
	resolved, err := e.checkPath(path)
	if err != nil {
		// Wrap with "blocked:" prefix for backward compat, preserving any
		// ErrWithSuggestion so callers can surface the suggestion field.
		var sErr *ErrWithSuggestion
		if errors.As(err, &sErr) {
			return "", &ErrWithSuggestion{
				Err:        fmt.Errorf("blocked: %w", sErr.Err),
				Suggestion: sErr.Suggestion,
			}
		}
		return "", fmt.Errorf("blocked: %w", err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &ErrWithSuggestion{
				Err:        fmt.Errorf("file not found: %s", path),
				Suggestion: "verify the path exists with list_dir on the parent, or check for typos",
			}
		}
		if os.IsPermission(err) {
			return "", &ErrWithSuggestion{
				Err:        fmt.Errorf("permission denied: %s", path),
				Suggestion: "file is not readable by the sandbox; check ownership or choose a different file",
			}
		}
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("not a regular file: %s", path),
			Suggestion: "target a regular file, not a directory — use list_dir to enumerate entries",
		}
	}
	if info.Size() > maxFileSize {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("file too large: %d MB (max 50 MB)", info.Size()>>20),
			Suggestion: "file is too large to read in one shot; use start_line/end_line to window, or search_files for a pattern",
		}
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsPermission(err) {
			return "", &ErrWithSuggestion{
				Err:        fmt.Errorf("permission denied: %s", path),
				Suggestion: "file is not readable by the sandbox; check ownership or choose a different file",
			}
		}
		return "", err
	}

	e.tracker.record(resolved, info.ModTime())

	check := data
	if len(check) > 512 {
		check = check[:512]
	}
	// Binary detection: return a plain result (not an error) with a suggestion
	// appended for LLM guidance — preserves backward compatibility.
	if strings.ContainsRune(string(check), 0) {
		return fmt.Sprintf("[binary file: %d bytes — use checksum_tree for integrity or skip content reads]", len(data)), nil
	}

	tail := 0
	if t, ok := args["tail"].(float64); ok && int(t) > 0 {
		tail = int(t)
	}

	startLine := 1
	endLine := startLine + 199
	if tail == 0 {
		if s, ok := args["start_line"].(float64); ok && int(s) >= 1 {
			startLine = int(s)
		}
		if el, ok := args["end_line"].(float64); ok && int(el) >= startLine {
			endLine = int(el)
		} else {
			endLine = startLine + 199
		}
	}

	lines := strings.Split(string(data), "\n")
	total := len(lines)
	if lines[total-1] == "" {
		total--
	}

	if tail > 0 {
		startLine = total - tail + 1
		if startLine < 1 {
			startLine = 1
		}
		endLine = total
	}

	if startLine > total {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("file has %d lines, start_line %d is past end", total, startLine),
			Suggestion: fmt.Sprintf("requested window starts beyond file length (%d lines); reduce start_line", total),
		}
	}
	if endLine > total {
		endLine = total
	}

	width := len(strconv.Itoa(endLine))
	var b strings.Builder
	for i := startLine - 1; i < endLine && i < len(lines); i++ {
		fmt.Fprintf(&b, "%*d\t%s\n", width, i+1, lines[i])
	}
	result := truncateOutput(strings.TrimRight(b.String(), "\n"), 200)
	if total > endLine {
		result += fmt.Sprintf("\n[file has %d lines; showing %d-%d. Use start_line=%d to continue]",
			total, startLine, endLine, endLine+1)
	}
	return result, nil
}
