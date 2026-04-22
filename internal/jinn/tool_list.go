package jinn

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

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

	if _, err := e.checkPath(listPath); err != nil {
		return "", err
	}

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
	result := strings.TrimSpace(out.String())
	if result == "" {
		return "(empty directory)", nil
	}
	if out.Truncated() {
		result += "\n[output truncated at 1 MB]"
	}
	return truncateOutput(result, 200), nil
}
