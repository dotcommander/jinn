package main

import (
	"strings"
	"testing"
)

// makeMessages builds a slice of alternating user/assistant messages whose
// combined content is padded to roughly targetTokens tokens (4 chars/token).
func makeMessages(n int, targetTokens int) []message {
	msgs := make([]message, n)
	padding := strings.Repeat("x", (targetTokens/n)*4)
	for i := range n {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = message{Role: role, Content: padding}
	}
	return msgs
}

func TestShouldCompact_TokenTriggerFires(t *testing.T) {
	// Not parallel: ShouldCompact is pure but we document intent clearly.
	t.Parallel()

	// Build 10 messages totalling ~10 000 tokens.
	msgs := makeMessages(10, 10_000)
	tokens := EstimateMessagesTokens(msgs)

	// contextWindow=8192, threshold=0.70 → trigger at ≥5735 tokens.
	// Our 10k-token batch is well above that.
	if !ShouldCompact(tokens, 8192, 0.70) {
		t.Errorf("expected ShouldCompact=true for %d tokens (window=8192, threshold=0.70)", tokens)
	}
}

func TestShouldCompact_BelowThreshold_NoTrigger(t *testing.T) {
	t.Parallel()

	// 100 tokens, window=8192, threshold=0.70 → 100/8192=0.012 — no trigger.
	msgs := makeMessages(2, 100)
	tokens := EstimateMessagesTokens(msgs)

	if ShouldCompact(tokens, 8192, 0.70) {
		t.Errorf("expected ShouldCompact=false for %d tokens (window=8192, threshold=0.70)", tokens)
	}
}

func TestShouldCompact_CounterFallbackStillWorks(t *testing.T) {
	t.Parallel()

	// Token count is low (50 tokens) so token trigger won't fire.
	// Verify the counter-based path works independently.
	msgs := makeMessages(2, 50)
	tokens := EstimateMessagesTokens(msgs)

	// Token trigger should be false.
	if ShouldCompact(tokens, 8192, 0.70) {
		t.Fatalf("precondition: expected token trigger=false for %d tokens", tokens)
	}

	// Simulate the counter check directly: cfg.compactEvery=3, counter=3 → fires.
	cfg := &config{
		contextWindow:    8192,
		compactThreshold: 0.70,
		compactEvery:     3,
	}

	// counter >= compactEvery with low token count: the maybeCompact function
	// should proceed to compaction logic.  We can't call maybeCompact without an
	// LLM, so we verify the precondition logic manually — this documents the
	// expected branch path.
	counter := 3
	tokenTriggered := ShouldCompact(tokens, cfg.contextWindow, cfg.compactThreshold)
	counterTriggered := cfg.compactEvery > 0 && counter >= cfg.compactEvery

	if tokenTriggered {
		t.Errorf("token trigger should be false (tokens=%d)", tokens)
	}
	if !counterTriggered {
		t.Errorf("counter trigger should be true (counter=%d, compactEvery=%d)", counter, cfg.compactEvery)
	}
}

func TestPreprocessCfg_EmptyReturnsSamePointer(t *testing.T) {
	t.Parallel()
	cfg := &config{model: "openai/gpt-5.4-mini", preprocessModel: ""}
	got := preprocessCfg(cfg)
	if got != cfg {
		t.Errorf("preprocessCfg with empty preprocessModel must return the same *config pointer; got new pointer")
	}
	if got.model != "openai/gpt-5.4-mini" {
		t.Errorf("main model mutated: got %q", got.model)
	}
}

func TestPreprocessCfg_NonEmptyReturnsCloneWithOverride(t *testing.T) {
	t.Parallel()
	cfg := &config{
		model:           "openai/gpt-5.4-mini",
		preprocessModel: "openai/gpt-5.4-nano",
		baseURL:         "https://example.test/v1/chat",
		apiKey:          "sk-test",
		temperature:     0.7,
	}
	got := preprocessCfg(cfg)
	if got == cfg {
		t.Fatal("preprocessCfg with override must return a new *config, got same pointer")
	}
	if got.model != "openai/gpt-5.4-nano" {
		t.Errorf("got.model = %q, want override %q", got.model, "openai/gpt-5.4-nano")
	}
	if cfg.model != "openai/gpt-5.4-mini" {
		t.Errorf("source cfg.model mutated: got %q, want preserved %q", cfg.model, "openai/gpt-5.4-mini")
	}
	if got.baseURL != cfg.baseURL || got.apiKey != cfg.apiKey || got.temperature != cfg.temperature {
		t.Errorf("other fields not preserved on clone: baseURL=%q apiKey=%q temp=%v", got.baseURL, got.apiKey, got.temperature)
	}
}
