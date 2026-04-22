package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const systemPromptTemplate = `You are a coding assistant with tool access via the jinn sandboxed executor.
Working directory: %s
OS: %s/%s

Tools:
- read_file, stat_file, list_dir, search_files: explore before modifying.
- edit_file / multi_edit: targeted text replacements. old_text MUST be unique in the file — include surrounding context if a snippet repeats.
- write_file: atomic full-file writes (creates parent dirs).
- run_shell: bash with a timeout (default 30s, max 300s). Prefer dry_run=true for destructive operations before committing.
- web_fetch: retrieve an HTTP(S) URL as markdown. Use for docs/articles, not local files.

Workflow:
1. Read before modifying. Use search_files or list_dir to orient.
2. Explain your plan in one or two sentences, then act.
3. If a tool returns an error, diagnose the cause (wrong path, non-unique match, syntax) before retrying.
4. When the task is complete, give a short summary and stop — do not call more tools.`

func systemPrompt(cfg *config) string {
	return fmt.Sprintf(systemPromptTemplate, cfg.workDir, runtime.GOOS, runtime.GOARCH)
}

// turnHooks decouples runTurns from presentation. All fields are optional.
// Content streams through OnContent as it arrives; tool calls are surfaced
// via OnToolCall before dispatch.
type turnHooks struct {
	OnContent   func(delta string)
	OnStreamEnd func(hadContent bool)
	OnToolCall  func(name, args string)
}

// runTurns drives the assistant through one or more turns until it stops
// calling tools or max-turns is hit. It streams content via hooks and returns
// the full message history (including assistant replies and tool results).
func runTurns(ctx context.Context, cfg *config, tools []map[string]any, messages []message, hooks turnHooks) ([]message, error) {
	for turn := 1; turn <= cfg.maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return messages, err
		}

		var hadContent bool
		final, _, err := chatStream(ctx, cfg, messages, tools, func(delta string) {
			hadContent = true
			if hooks.OnContent != nil {
				hooks.OnContent(delta)
			}
		})
		if err != nil {
			return messages, err
		}
		if hooks.OnStreamEnd != nil {
			hooks.OnStreamEnd(hadContent)
		}

		messages = append(messages, final)
		_ = saveSession(cfg, messages)

		if len(final.ToolCalls) == 0 {
			return messages, nil
		}

		for _, tc := range final.ToolCalls {
			if hooks.OnToolCall != nil {
				hooks.OnToolCall(tc.Function.Name, tc.Function.Arguments)
			}
			result, err := dispatchTool(ctx, cfg, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				result = fmt.Sprintf("dispatch error: %v", err)
			}
			messages = append(messages, message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
		_ = saveSession(cfg, messages)
	}
	return messages, fmt.Errorf("max turns reached (%d) without completion", cfg.maxTurns)
}

// runOneShot executes a single prompt-to-completion pass. Used when demo
// is invoked with an argument or piped stdin.
func runOneShot(ctx context.Context, cfg *config, messages []message) error {
	tools, err := fetchToolsSchema(ctx, cfg)
	if err != nil {
		return err
	}

	hooks := turnHooks{
		OnContent: func(delta string) { fmt.Print(delta) },
		OnStreamEnd: func(hadContent bool) {
			if hadContent {
				fmt.Println()
			}
		},
		OnToolCall: func(name, args string) {
			if cfg.quiet {
				return
			}
			fmt.Fprintf(os.Stderr, "  [%s] %s\n", name, previewArgs(args))
		},
	}

	_, err = runTurns(ctx, cfg, tools, messages, hooks)
	return err
}

func previewArgs(argsJSON string) string {
	const max = 120
	s := strings.TrimSpace(argsJSON)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func saveSession(cfg *config, messages []message) error {
	if cfg.sessionID == "" {
		return nil
	}
	if err := os.MkdirAll(cfg.sessionDir, 0o755); err != nil {
		return fmt.Errorf("mkdir session dir: %w", err)
	}
	path := filepath.Join(cfg.sessionDir, cfg.sessionID+".json")
	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

func loadSession(cfg *config) ([]message, error) {
	path := filepath.Join(cfg.sessionDir, cfg.sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var msgs []message
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, fmt.Errorf("decode session %s: %w", path, err)
	}
	return msgs, nil
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
