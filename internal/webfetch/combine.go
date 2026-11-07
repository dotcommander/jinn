package webfetch

import (
	"context"
	"net/url"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"
)

const (
	rrfK             = 60
	targetUsable     = 3
	errgroupMaxBatch = targetUsable
)

func combineCeil(limit int) int {
	// ceil(limit / targetUsable), at least 1.
	per := (limit + targetUsable - 1) / targetUsable
	return combineMax(per, 1)
}

// backendCombineResults holds one backend's contribution to a combine search.
type backendCombineResults struct {
	backend string
	results []SearchResult
	err     error
}

// combineSearch runs the ordered chain in targeted batches: it stops once
// targetUsable backends have answered, so combine never pays for the whole
// chain when early backends suffice. Results are ranked by Reciprocal Rank
// Fusion (k=60) and deduplicated by normalized URL, preferring the richest
// result; each hit carries its source backends.
func (s *Service) combineSearch(ctx context.Context, chain []searchProvider, req SearchRequest) (SearchResponse, error) {
	perBackend := combineCeil(req.Limit)
	var collected []backendCombineResults
	var fallbacks []SearchFallback
	cursor := 0
	for len(collected) < targetUsable && cursor < len(chain) {
		needed := targetUsable - len(collected)
		remaining := len(chain) - cursor
		batchSize := combineMin(needed, remaining)
		batch := chain[cursor : cursor+batchSize]
		cursor += batchSize

		group, groupCtx := errgroup.WithContext(ctx)
		group.SetLimit(errgroupMaxBatch)
		results := make([]backendCombineResults, len(batch))
		for i, provider := range batch {
			group.Go(func() error {
				hits, err := provider.search(groupCtx, SearchRequest{
					Query: req.Query, Limit: perBackend, Category: req.Category,
					IncludeDomains: req.IncludeDomains, StartPublishedDate: req.StartPublishedDate,
					IncludeHighlights: req.IncludeHighlights, HighlightSentences: req.HighlightSentences,
				})
				results[i] = backendCombineResults{backend: provider.name(), results: hits, err: err}
				return nil
			})
		}
		_ = group.Wait()
		for _, result := range results {
			switch {
			case result.err == nil && len(result.results) > 0:
				collected = append(collected, result)
			case result.err != nil:
				fallbacks = append(fallbacks, SearchFallback{Provider: result.backend, Reason: searchErrorReason(result.err)})
			default:
				reason := fallbackReasonEmpty
				if len(req.IncludeDomains) > 0 {
					reason = fallbackReasonDomainMiss
				}
				fallbacks = append(fallbacks, SearchFallback{Provider: result.backend, Reason: reason})
			}
		}
	}
	if len(collected) == 0 && len(fallbacks) > 0 {
		return SearchResponse{}, errLastFallback(fallbacks)
	}
	merged := reciprocalRankFusion(collected, req.Limit)
	return SearchResponse{
		Query:     req.Query,
		Results:   merged,
		Fallbacks: fallbacks,
		Combine:   true,
	}, nil
}

func errLastFallback(fallbacks []SearchFallback) error {
	last := fallbacks[len(fallbacks)-1]
	return newCodedError(errReason(last.Reason), ErrorCodeProviderResponse, "retry with a different provider list")
}

func errReason(reason string) error {
	if reason == "empty" || reason == "domain-miss" {
		return errEmptyResults
	}
	return errCombineFailed
}

var errEmptyResults = errorString("no backend returned results")
var errCombineFailed = errorString("every backend failed")

type errorString string

func (e errorString) Error() string { return string(e) }

// reciprocalRankFusion merges backend results by score sum(1/(k+rank)).
// Dedup keeps the richest result and records every source backend.
func reciprocalRankFusion(batches []backendCombineResults, maxResults int) []SearchResult {
	if len(batches) == 1 {
		results := batches[0].results
		if len(results) > maxResults {
			results = results[:maxResults]
		}
		for i := range results {
			results[i].Sources = []string{batches[0].backend}
		}
		return results
	}
	type fused struct {
		score    float64
		result   SearchResult
		backends []string
	}
	scores := map[string]*fused{}
	order := []string{}
	for _, batch := range batches {
		for rank, result := range batch.results {
			key := normalizeResultURL(result.URL)
			entry, ok := scores[key]
			if !ok {
				entry = &fused{}
				scores[key] = entry
				order = append(order, key)
			}
			entry.score += 1.0 / float64(rrfK+rank+1)
			entry.backends = appendUnique(entry.backends, batch.backend)
			if richness(result) > richness(entry.result) {
				result.Sources = entry.backends
				entry.result = result
			} else {
				entry.result.Sources = entry.backends
			}
		}
	}
	entries := make([]*fused, 0, len(order))
	for _, key := range order {
		entries = append(entries, scores[key])
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].score != entries[j].score {
			return entries[i].score > entries[j].score
		}
		return len(entries[i].backends) > len(entries[j].backends)
	})
	if len(entries) > maxResults {
		entries = entries[:maxResults]
	}
	merged := make([]SearchResult, 0, len(entries))
	for _, entry := range entries {
		merged = append(merged, entry.result)
	}
	return merged
}

func normalizeResultURL(raw string) string {
	if parsed, err := url.Parse(strings.TrimSpace(raw)); err == nil {
		parsed.Fragment = ""
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		if parsed.Path == "" {
			parsed.Path = "/"
		}
		return strings.ToLower(parsed.String())
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

func richness(result SearchResult) int {
	return len(result.Description) + len(strings.Join(result.Highlights, " "))
}

func appendUnique(items []string, item string) []string {
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

func combineMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func combineMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}
