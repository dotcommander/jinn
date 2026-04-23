package jinn

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const (
	listDefaultMax = 500
	listCapMax     = 10000
)

// listDirResult is the structured response for list_dir, including truncation metadata.
type listDirResult struct {
	Entries    []string `json:"entries"`
	Truncated  bool     `json:"truncated"`
	TotalCount int      `json:"total_count"`
}

func (e *Engine) listDir(args map[string]interface{}) (string, error) {
	listPath := "."
	if p, ok := args["path"].(string); ok && p != "" {
		listPath = p
	}
	depth := 3
	if d, ok := args["depth"].(float64); ok {
		depth = int(d)
	}
	if depth < 1 {
		depth = 1
	}
	if depth > 10 {
		depth = 10
	}

	maxEntries := intArg(args, "max_entries", listDefaultMax)
	if maxEntries > listCapMax {
		maxEntries = listCapMax
	}

	if _, err := e.checkPath(listPath); err != nil {
		return "", err
	}

	// Collect all entries via find | sort into a single buffer.
	out := &boundedWriter{limit: 1 << 20}
	c := exec.Command("find", listPath, "-maxdepth", strconv.Itoa(depth), "-not", "-path", "*/.*")
	c.Dir = e.workDir
	c.Stderr = out
	sortCmd := exec.Command("sort")
	pipe, err := c.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("listDir: pipe: %w", err)
	}
	sortCmd.Stdin = pipe
	sortCmd.Stdout = out
	sortCmd.Stderr = out
	if err := sortCmd.Start(); err != nil {
		return "", fmt.Errorf("listDir: sort: %w", err)
	}
	c.Run()
	sortCmd.Wait()

	raw := strings.TrimSpace(out.String())
	if raw == "" {
		res := listDirResult{Entries: []string{}, Truncated: false, TotalCount: 0}
		b, _ := json.Marshal(res)
		return string(b), nil
	}

	all := strings.Split(raw, "\n")
	total := len(all)
	truncated := total > maxEntries

	shown := all
	if truncated {
		shown = all[:maxEntries]
	}

	res := listDirResult{
		Entries:    shown,
		Truncated:  truncated,
		TotalCount: total,
	}
	b, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("listDir: marshal: %w", err)
	}
	result := string(b)
	if truncated {
		result += "\n" + formatTruncatedHint(maxEntries, total, "'max_entries' or 'pattern'")
	}
	if out.Truncated() {
		result += "\n[output truncated at 1 MB]"
	}
	return result, nil
}
