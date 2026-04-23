package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// fetchToolsSchema calls `jinn --schema`, drops the internal list_tools entry,
// and appends the web_fetch tool (handled by defuddle, not jinn).
func fetchToolsSchema(ctx context.Context, cfg *config) ([]map[string]any, error) {
	schemaCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(schemaCtx, cfg.jinnBin, "--schema")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("jinn --schema: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	var tools []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &tools); err != nil {
		return nil, fmt.Errorf("parse jinn schema: %w", err)
	}

	tools = slices.DeleteFunc(tools, func(t map[string]any) bool {
		fn, _ := t["function"].(map[string]any)
		return fn["name"] == "list_tools"
	})
	tools = append(tools, webFetchSchema())
	return tools, nil
}

func webFetchSchema() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "web_fetch",
			"description": "Fetch an HTTP(S) URL and return its main content as markdown (via the defuddle extractor). Use for reading documentation, articles, and reference material. Not for local files — use read_file instead.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "Absolute http:// or https:// URL.",
					},
				},
				"required":             []string{"url"},
				"additionalProperties": false,
			},
		},
	}
}

// dispatchTool routes a single tool call. web_fetch is handled locally via
// defuddle; everything else is forwarded verbatim to the jinn binary.
func dispatchTool(ctx context.Context, cfg *config, name, argsJSON string) (string, error) {
	if argsJSON == "" {
		argsJSON = "{}"
	}

	if cfg.dryRun {
		switch name {
		case "write_file", "edit_file", "multi_edit":
			return fmt.Sprintf("[DRY RUN] Would execute %s with arguments: %s", name, argsJSON), nil
		case "run_shell":
			var args map[string]any
			if err := json.Unmarshal([]byte(argsJSON), &args); err == nil {
				args["dry_run"] = true
				if b, err := json.Marshal(args); err == nil {
					argsJSON = string(b)
				}
			}
		}
	}

	if name == "web_fetch" {
		return runDefuddle(ctx, cfg, argsJSON)
	}
	return runJinn(ctx, cfg, name, argsJSON)
}

func runJinn(ctx context.Context, cfg *config, tool, argsJSON string) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse tool arguments: %w", err)
	}
	payload, err := json.Marshal(map[string]any{"tool": tool, "args": args})
	if err != nil {
		return "", fmt.Errorf("marshal jinn request: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(callCtx, cfg.jinnBin)
	cmd.Dir = cfg.workDir
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("jinn exit %d: %s", exitErr.ExitCode(), strings.TrimSpace(stderr.String()))
		}
		return "", fmt.Errorf("jinn: %w", err)
	}

	var resp struct {
		OK     bool   `json:"ok"`
		Result string `json:"result"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return "", fmt.Errorf("parse jinn response: %w (output: %s)", err, truncate(stdout.String(), 200))
	}
	if !resp.OK {
		return fmt.Sprintf("tool error: %s", resp.Error), nil
	}
	return resp.Result, nil
}

func runDefuddle(ctx context.Context, cfg *config, argsJSON string) (string, error) {
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse web_fetch arguments: %w", err)
	}
	if !strings.HasPrefix(args.URL, "http://") && !strings.HasPrefix(args.URL, "https://") {
		return "", fmt.Errorf("web_fetch requires absolute http(s) URL, got %q", args.URL)
	}

	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(callCtx, cfg.defuddleBin, "parse", args.URL, "--markdown")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Sprintf("web_fetch error (exit %d): %s", exitErr.ExitCode(), truncate(strings.TrimSpace(stderr.String()), 400)), nil
		}
		return "", fmt.Errorf("defuddle: %w", err)
	}
	return stdout.String(), nil
}
