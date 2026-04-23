package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed prompts/compact.md
var defaultCompactPrompt string

// compactHistory replaces the older turns of `messages` with a single
// assistant-role summary. Expects messages in the canonical layout:
//
//	[system, ...older..., latestUser]
//
// Returns a new slice: [system, assistant(summary), latestUser]. The caller
// owns whether to use the compacted slice or fall back on the original.
//
// On LLM failure, returns the original slice and a non-nil error — the caller
// should log a warning and proceed uncompacted.
func compactHistory(ctx context.Context, cfg *config, messages []message, prompt string) ([]message, error) {
	if len(messages) < 3 {
		return messages, nil // nothing meaningful to compact
	}
	system := messages[0]
	if system.Role != "system" {
		return messages, fmt.Errorf("compact: expected system at index 0, got %q", system.Role)
	}
	latestUser := messages[len(messages)-1]
	if latestUser.Role != "user" {
		return messages, fmt.Errorf("compact: expected user at last index, got %q", latestUser.Role)
	}
	middle := messages[1 : len(messages)-1]
	if len(middle) == 0 {
		return messages, nil
	}

	historyJSON, err := json.MarshalIndent(middle, "", "  ")
	if err != nil {
		return messages, fmt.Errorf("compact: marshal history: %w", err)
	}

	summaryReq := []message{
		{Role: "system", Content: "You are a conversation summarizer. Follow the user's instructions exactly."},
		{Role: "user", Content: prompt + "\n\nConversation history (JSON):\n\n" + string(historyJSON)},
	}

	// tools=nil ensures the model emits plain text, not tool calls.
	// onContent=nil — we don't stream the summary to the user.
	// preprocessCfg routes to cfg.preprocessModel when set; else uses cfg as-is.
	reply, _, err := chatStream(ctx, preprocessCfg(cfg), summaryReq, nil, nil, nil)
	if err != nil {
		return messages, fmt.Errorf("compact: llm: %w", err)
	}
	if reply.Content == "" {
		return messages, fmt.Errorf("compact: empty summary from model")
	}

	summaryMsg := message{
		Role:    "assistant",
		Content: "Conversation summary:\n\n" + reply.Content,
	}
	return []message{system, summaryMsg, latestUser}, nil
}
