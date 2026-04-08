package jinn

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var grepExcludeDirs = []string{".git", "node_modules", "vendor", "__pycache__", ".cache", "dist", "build"}

func (e *Engine) searchFiles(args map[string]interface{}) (string, error) {
	pattern, _ := args["pattern"].(string)
	searchPath := "."
	if p, ok := args["path"].(string); ok && p != "" {
		searchPath = p
	}

	if _, err := e.checkPath(searchPath); err != nil {
		return "", err
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return "", fmt.Errorf("invalid regex: %s", err)
	}

	grepArgs := []string{"-r", "-n"}
	for _, dir := range grepExcludeDirs {
		grepArgs = append(grepArgs, "--exclude-dir="+dir)
	}
	if include, ok := args["include"].(string); ok && include != "" {
		grepArgs = append(grepArgs, "--include="+include)
	}
	if ctx, ok := args["context_lines"].(float64); ok && int(ctx) > 0 {
		grepArgs = append(grepArgs, "-C", strconv.Itoa(int(ctx)))
	}
	if ci, ok := args["case_insensitive"].(bool); ok && ci {
		grepArgs = append(grepArgs, "-i")
	}
	grepArgs = append(grepArgs, "--", pattern, searchPath)

	out := &boundedWriter{limit: 1 << 20}
	c := exec.Command("grep", grepArgs...)
	c.Dir = e.workDir
	c.Stdout = out
	c.Stderr = out
	c.Run()

	raw := out.String()
	if raw != "" {
		lines := strings.Split(raw, "\n")
		for i, l := range lines {
			lines[i] = truncateLine(l, 200)
		}
		raw = strings.Join(lines, "\n")
	}
	if out.Truncated() {
		raw += "\n[output truncated at 1 MB]"
	}
	return truncateOutput(raw, 100), nil
}
