package webfetch

import (
	"testing"
)

func TestExtractiveSummaryIsDeterministicAndCited(t *testing.T) {
	t.Parallel()
	results := []SearchResult{
		{Title: "Go releases", URL: "https://go.dev", Description: "Go 1.26 released with new features."},
		{Title: "Go news", URL: "https://go.dev/news", Highlights: []string{"Goroutines made easy."}},
	}
	first := extractiveSummary(results)
	second := extractiveSummary(results)
	if first == nil || first.Status != "ok" {
		t.Fatalf("summary = %+v", first)
	}
	if first.Text != second.Text {
		t.Fatal("summary is not deterministic")
	}
	if len(first.Citations) != 2 || first.Citations[0] != 1 || first.Citations[1] != 2 {
		t.Fatalf("citations = %v", first.Citations)
	}
}

func TestExtractiveSummaryInsufficientEvidence(t *testing.T) {
	t.Parallel()
	summary := extractiveSummary(nil)
	if summary == nil || summary.Status != "insufficient_evidence" || summary.Text != "" {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestExtractiveSummaryBoundsSnippets(t *testing.T) {
	t.Parallel()
	long := make([]rune, 500)
	for i := range long {
		long[i] = 'x'
	}
	results := make([]SearchResult, 0, 8)
	for i := 0; i < 8; i++ {
		results = append(results, SearchResult{Title: "T", URL: "https://example.com", Description: string(long)})
	}
	summary := extractiveSummary(results)
	if summary == nil || summary.Status != "ok" {
		t.Fatalf("summary = %+v", summary)
	}
	if len(summary.Citations) > maxSummaryResults {
		t.Fatalf("citations = %d, want <= %d", len(summary.Citations), maxSummaryResults)
	}
	for _, line := range splitLinesForTest(summary.Text) {
		if len([]rune(line)) > maxSummarySnippet+40 {
			t.Fatalf("line too long (%d runes): %q", len([]rune(line)), line)
		}
	}
}

func splitLinesForTest(text string) []string {
	var lines []string
	start := 0
	for i, r := range text {
		if r == '\n' {
			lines = append(lines, text[start:i])
			start = i + 1
		}
	}
	return append(lines, text[start:])
}
