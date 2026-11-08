package webfetch

import (
	"fmt"
	"strings"
)

// Extractive search summaries (slice 4): a deterministic, cited digest
// assembled only from source-provided result text. No model is called, so
// identical results always produce identical summaries. Summary contract
// (decided 2026-11-08): the caller synthesizes from this digest and the
// underlying results; jinn core has no model adapter, and model-backed
// synthesis (pi-web-access's Luna workflow) is not silently substituted.

const (
	maxSummaryResults = 5
	maxSummarySnippet = 200
)

// extractiveSummary builds the digest for one result set. With no usable
// evidence it returns a labeled insufficient_evidence status with no text,
// leaving the underlying results intact.
func extractiveSummary(results []SearchResult) *SearchSummary {
	var lines []string
	var citations []int
	for i, result := range results {
		if len(lines) >= maxSummaryResults {
			break
		}
		snippet := summarySnippet(result)
		if snippet == "" && strings.TrimSpace(result.Title) == "" {
			continue
		}
		citations = append(citations, i+1)
		where := result.URL
		if where == "" {
			where = "(no URL)"
		}
		if snippet != "" {
			lines = append(lines, fmt.Sprintf("[%d] %s — %s: %s", i+1, result.Title, where, snippet))
		} else {
			lines = append(lines, fmt.Sprintf("[%d] %s — %s", i+1, result.Title, where))
		}
	}
	if len(lines) == 0 {
		return &SearchSummary{Status: "insufficient_evidence"}
	}
	return &SearchSummary{
		Status:    "ok",
		Text:      strings.Join(lines, "\n"),
		Citations: citations,
	}
}

func summarySnippet(result SearchResult) string {
	for _, candidate := range append(append([]string{}, result.Highlights...), result.Description) {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			if len([]rune(trimmed)) > maxSummarySnippet {
				return string([]rune(trimmed)[:maxSummarySnippet]) + "…"
			}
			return trimmed
		}
	}
	return ""
}
