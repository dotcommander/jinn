package main

import (
	"context"
	_ "embed"
	"errors"
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

// errCompactionCanceled signals that compaction was interrupted (Ctrl-C).
// Callers should discard the old ctx and derive a fresh one from the REPL's
// root signal source before proceeding with the turn.
var errCompactionCanceled = errors.New("compaction canceled")

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
	Timer        *toolTimer
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

		// previews tracks one diffPreview per tool-call index within this turn.
		// Keyed by tool-call stream index; created lazily on first arg delta.
		previews := map[int]*diffPreview{}

		var hadContent bool
		final, _, err := chatStream(ctx, cfg, messages, tools, func(delta string) {
			hadContent = true
			if hooks.OnContent != nil {
				hooks.OnContent(delta)
			}
		}, func(idx int, name, delta string) {
			if !cfg.previewDiffs {
				return
			}
			if name != "edit_file" && name != "write_file" {
				// Name may arrive in the first chunk; once we know it's not a
				// diff-preview target, gate out. If name is "" (not yet known),
				// we buffer anyway — Reset() is cheap and Feed() is a no-op when
				// the preview turns out to not be needed.
				if p, ok := previews[idx]; ok {
					_ = p // already created; keep buffering until name is confirmed
				}
				if name != "" {
					// Name is now confirmed non-edit — discard any buffered preview.
					delete(previews, idx)
					return
				}
			}
			p, ok := previews[idx]
			if !ok {
				p = newDiffPreview(true)
				previews[idx] = p
			}
			p.Feed(delta)
			p.Render()
		})
		if err != nil {
			return messages, err
		}
		if hooks.OnStreamEnd != nil {
			hooks.OnStreamEnd(hadContent)
		}
		// Final render + cleanup for any active diff previews.
		for _, p := range previews {
			p.Render()
			p.Reset()
		}

		messages = append(messages, final)
		_ = saveSession(cfg, messages)

		if len(final.ToolCalls) == 0 {
			return messages, nil
		}

		results := make([]message, len(final.ToolCalls))
		names := make([]string, len(final.ToolCalls))
		for i, tc := range final.ToolCalls {
			names[i] = tc.Function.Name
		}
		timer := hooks.Timer
		if timer != nil {
			timer.SetNames(names)
			timer.Start()
		}
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
				if timer != nil {
					timer.Finish(i)
				}
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
		if timer != nil {
			timer.Stop()
		}

		for _, res := range results {
			messages = append(messages, res)
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

	p := newPalette(useColor())
	md := newMDStream(os.Stdout, p)

	var mu sync.Mutex
	var timer *toolTimer
	if !cfg.quiet {
		timer = newToolTimer(os.Stderr, p, &mu)
	}
	hooks := turnHooks{
		Timer:     timer,
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

// maybeCompact summarizes older history when the token budget or user-turn
// counter triggers compaction. Token-budget check takes precedence; counter
// falls back for users who don't configure contextWindow.
//
// Trigger order:
//  1. Token budget: ShouldCompact(tokens, cfg.contextWindow, cfg.compactThreshold)
//  2. Counter fallback: counter >= cfg.compactEvery (when compactEvery > 0)
//
// Behavior on failure:
//   - ctx.Canceled: returns sentinel errCompactionCanceled — caller must
//     derive a fresh turnCtx before continuing (see repl.go).
//   - any other error: logs a warning, returns original messages, returns nil
//     (the turn proceeds with full history).
//
// Callers increment the counter BEFORE calling, then treat the returned
// counter as authoritative (it is reset to 0 on a successful compaction).
func maybeCompact(ctx context.Context, cfg *config, messages []message, counter int) ([]message, int, error) {
	tokensBefore := EstimateMessagesTokens(messages)
	tokenTriggered := ShouldCompact(tokensBefore, cfg.contextWindow, cfg.compactThreshold)
	if !tokenTriggered {
		// Fall through to counter-based check.
		if cfg.compactEvery <= 0 || counter < cfg.compactEvery {
			return messages, counter, nil
		}
	}

	p := newPalette(useColor())
	var spin *spinner
	var spinMu sync.Mutex
	if stderrIsTTY() {
		spin = newSpinner(os.Stderr, p, &spinMu)
		spin.start()
	}

	start := time.Now()
	compacted, err := compactHistory(ctx, cfg, messages, cfg.compactPrompt)
	elapsed := time.Since(start)

	if spin != nil {
		spin.halt()
	}

	if err != nil {
		if errors.Is(err, context.Canceled) {
			if stderrIsTTY() {
				fmt.Fprintf(os.Stderr, "%s⚠ compaction canceled — continuing with full history%s\n", p.dim, p.reset)
			}
			return messages, counter, errCompactionCanceled
		}
		if stderrIsTTY() {
			fmt.Fprintf(os.Stderr, "%s⚠ compaction skipped: %s%s\n", p.dim, shortErr(err), p.reset)
		} else {
			fmt.Fprintf(os.Stderr, "warning: compaction failed: %v — continuing with full history\n", err)
		}
		return messages, counter, nil
	}

	tokensAfter := EstimateMessagesTokens(compacted)
	if stderrIsTTY() {
		fmt.Fprintf(os.Stderr, "%s%s✓ history compacted · %.1fs%s\n", p.dim, p.success, elapsed.Seconds(), p.reset)
	}
	fmt.Fprintln(os.Stderr, FormatCompactLog(tokensBefore, tokensAfter))
	return compacted, 0, nil
}

// shortErr extracts a compact reason from a wrapped error for status display.
// Keeps the last colon-separated segment, trimmed. Falls back to full string.
func shortErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && i+2 < len(s) {
		s = s[i+2:]
	}
	return truncate(s, 80)
}
