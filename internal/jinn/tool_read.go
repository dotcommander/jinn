package jinn

import (
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
		return "", fmt.Errorf("blocked: %s", err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", path)
		}
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file: %s", path)
	}
	if info.Size() > maxFileSize {
		return "", fmt.Errorf("file too large: %d MB (max 50 MB)", info.Size()>>20)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", err
	}

	e.tracker.record(resolved, info.ModTime())

	check := data
	if len(check) > 512 {
		check = check[:512]
	}
	if strings.ContainsRune(string(check), 0) {
		return fmt.Sprintf("[binary file: %d bytes]", len(data)), nil
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
		return "", fmt.Errorf("file has %d lines, start_line %d is past end", total, startLine)
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
