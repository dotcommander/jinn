package main

import "fmt"

// DefaultContextWindow is the assumed max token budget when the user
// does not configure one explicitly.
const DefaultContextWindow = 8192

// DefaultCompactThreshold is the fraction of ContextWindow at which
// maybeCompact should trigger. 0.70 = compact when 70% full.
const DefaultCompactThreshold = 0.70

// EstimateTokens returns an approximate token count for s.
// Heuristic: len(s)/4. GPT-family rough rule. Empty string = 0.
// Deliberately imprecise and zero-cost — no tokenizer dependency.
func EstimateTokens(s string) int {
	return len(s) / 4
}

// EstimateMessagesTokens sums EstimateTokens for every content field in
// messages. Each message adds 4 tokens of overhead (role tag + separators)
// in addition to its content length. Empty content still contributes overhead.
func EstimateMessagesTokens(messages []message) int {
	const overhead = 4
	total := 0
	for _, m := range messages {
		total += overhead + EstimateTokens(m.Content)
	}
	return total
}

// ShouldCompact returns true if tokenCount/window >= threshold.
// Returns false when window <= 0 or threshold <= 0 (feature disabled).
func ShouldCompact(tokenCount, window int, threshold float64) bool {
	if window <= 0 || threshold <= 0 {
		return false
	}
	return float64(tokenCount)/float64(window) >= threshold
}

// FormatCompactLog produces the human-readable compaction log line:
//
//	"✓ history compacted · 1247→456 tokens · 62% reduction"
//
// Returns "" when before <= 0 to avoid division-by-zero.
// When after >= before the reduction is reported as 0%.
func FormatCompactLog(before, after int) string {
	if before <= 0 {
		return ""
	}
	reduction := 0
	if after < before {
		reduction = int(float64(before-after) / float64(before) * 100)
	}
	return fmt.Sprintf("✓ history compacted · %d→%d tokens · %d%% reduction", before, after, reduction)
}
