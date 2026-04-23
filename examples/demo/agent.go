package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed AGENTS.md
var systemPromptTemplate string

// applyPromptTokens substitutes {{workdir}} and {{os}} in a prompt template.
func applyPromptTokens(tmpl, workDir string) string {
	s := strings.ReplaceAll(tmpl, "{{workdir}}", workDir)
	s = strings.ReplaceAll(s, "{{os}}", runtime.GOOS+"/"+runtime.GOARCH)
	return s
}

// systemPrompt loads AGENTS.md from cfg.workDir when present; warns on
// unexpected I/O errors; silent on NotExist (falls back to embedded default).
func systemPrompt(cfg *config) string {
	agentsPath := filepath.Join(cfg.workDir, "AGENTS.md")
	data, err := os.ReadFile(agentsPath)
	if err == nil {
		return applyPromptTokens(string(data), cfg.workDir)
	}
	if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warning: could not read %s: %v — using embedded default\n", agentsPath, err)
	}
	return applyPromptTokens(systemPromptTemplate, cfg.workDir)
}

// turnHooks decouples runTurns from presentation. All fields are optional.
// Content streams through OnContent as it arrives; tool calls are surfaced
// via OnToolCall before dispatch.
type turnHooks struct {
	OnContent    func(delta string)
	OnStreamEnd  func(hadContent bool)
	OnToolCall   func(name, args string)
	OnToolResult func(name string, elapsed time.Duration, err error)
	BeforeTurn   func()
}

// runTurns drives the assistant through one or more turns until it stops
// calling tools or max-turns is hit. It streams content via hooks and returns
// the full message history (including assistant replies and tool results).
func runTurns(ctx context.Context, cfg *config, tools []map[string]any, messages []message, hooks turnHooks) ([]message, error) {
	for turn := 1; turn <= cfg.maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return messages, err
		}

		if hooks.BeforeTurn != nil {
			hooks.BeforeTurn()
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

		results := make([]message, len(final.ToolCalls))
		var wg sync.WaitGroup
		for i, tc := range final.ToolCalls {
			wg.Add(1)
			go func(i int, tc toolCall) {
				defer wg.Done()
				if hooks.OnToolCall != nil {
					hooks.OnToolCall(tc.Function.Name, tc.Function.Arguments)
				}
				start := time.Now()
				result, err := dispatchTool(ctx, cfg, tc.Function.Name, tc.Function.Arguments)
				elapsed := time.Since(start)
				if hooks.OnToolResult != nil {
					hooks.OnToolResult(tc.Function.Name, elapsed, err)
				}
				if err != nil {
					result = fmt.Sprintf("dispatch error: %v", err)
				}
				if len(result) > cfg.maxToolOutput {
					result = fmt.Sprintf("%s\n\n[TRUNCATED: original size %d bytes, showing first %d bytes]",
						result[:cfg.maxToolOutput], len(result), cfg.maxToolOutput)
				}
				results[i] = message{
					Role:       "tool",
					Content:    result,
					ToolCallID: tc.ID,
				}
			}(i, tc)
		}
		wg.Wait()

		for _, res := range results {
			messages = append(messages, res)
		}
		messages = pruneMessages(messages, cfg.maxHistory)
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

	p := newPalette(useColor())
	md := newMDStream(os.Stdout, p)

	hooks := turnHooks{
		OnContent: func(delta string) { md.Write(delta) },
		OnStreamEnd: func(hadContent bool) {
			md.Flush()
			if hadContent {
				fmt.Println()
			}
		},
		OnToolCall: func(name, args string) {
			if cfg.quiet {
				return
			}
			fmt.Fprintf(os.Stderr, "%s  · %s%s%s %s%s%s\n",
				p.dim,
				p.tool, name, p.reset,
				p.dim, filterToolArgs(name, args), p.reset,
			)
		},
	}

	_, err = runTurns(ctx, cfg, tools, messages, hooks)
	return err
}

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

// pruneMessages keeps the system prompt (if present) and the last max messages.
// It ensures that if a tool message is kept, the preceding assistant message
// that initiated the tool call is also kept.
func pruneMessages(msgs []message, max int) []message {
	if len(msgs) <= max || max <= 0 {
		return msgs
	}

	var kept []message
	if len(msgs) > 0 && msgs[0].Role == "system" {
		kept = append(kept, msgs[0])
		msgs = msgs[1:]
	}

	start := len(msgs) - max
	if start < 0 {
		start = 0
	}

	// If the first kept message is a 'tool' result, we must also keep the
	// 'assistant' message that preceded it.
	for start > 0 && msgs[start].Role == "tool" {
		start--
	}

	return append(kept, msgs[start:]...)
}
