package main

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
)

//go:embed prompts/rewriter.md
var defaultRewriterPrompt string

// shouldRewrite reports whether raw should be sent through the CRISP rewriter.
// Skip REPL meta commands (lines beginning with "/") and very short inputs —
// they're either not prompts or too small to benefit from structure.
func shouldRewrite(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "/") {
		return false
	}
	return len(strings.Fields(raw)) >= 8
}

// rewriteUserInput sends raw through the rewriter LLM and returns the
// rewritten prompt. Returns the rewritten text and nil on success.
// Caller is responsible for deciding whether to call this (see shouldRewrite)
// and for falling back to raw input on error.
func rewriteUserInput(ctx context.Context, cfg *config, prompt, raw string) (string, error) {
	req := []message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: raw},
	}
	// tools=nil — no tool calls. onContent=nil — do not stream to user.
	reply, _, err := chatStream(ctx, cfg, req, nil, nil)
	if err != nil {
		return "", fmt.Errorf("rewriter: %w", err)
	}
	out := strings.TrimSpace(reply.Content)
	if out == "" {
		return "", fmt.Errorf("rewriter: empty response")
	}
	return out, nil
}
