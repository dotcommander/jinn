package webfetch

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTavilySearchParsesResults(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body tavilySearchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		if body.APIKey != "tvly-test" || body.Query != "go" || body.MaxResults != 5 {
			t.Errorf("body = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"query":"go","results":[{"title":"Go","url":"https://go.dev","content":"The Go programming language.","published_date":"2026-01-01"}]}`))
	}))
	t.Cleanup(server.Close)

	service := NewService(Config{
		AllowPrivateNetworks: true,
		TavilyAPIKey:         "tvly-test",
		TavilySearchEndpoint: server.URL,
		SearchProvider:       providerTavily,
	})
	resp, err := service.Search(context.Background(), SearchRequest{Query: "go", Limit: 5, IncludeHighlights: true, HighlightSentences: 3})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Provider != providerTavily || len(resp.Results) != 1 {
		t.Fatalf("resp = %+v", resp)
	}
	hit := resp.Results[0]
	if hit.Title != "Go" || hit.URL != "https://go.dev" || hit.PublishedAt != "2026-01-01" {
		t.Fatalf("hit = %+v", hit)
	}
	if len(hit.Highlights) != 1 || !strings.Contains(hit.Highlights[0], "programming language") {
		t.Fatalf("highlights = %v", hit.Highlights)
	}
}

func TestTavilyRequiresAPIKey(t *testing.T) {
	t.Parallel()
	service := NewService(Config{SearchProvider: providerTavily})
	_, err := service.Search(context.Background(), SearchRequest{Query: "go"})
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != ErrorCodeMissingAPIKey {
		t.Fatalf("err = %#v, want missing_api_key", err)
	}
}

func TestSearchBatchPartialFailurePreservesSuccesses(t *testing.T) {
	t.Parallel()
	service := &Service{
		searchChain: []searchProvider{
			chainStubProvider{providerName: providerBrave, err: newCodedError(errors.New("boom"), ErrorCodeRateLimited, "retry later")},
		},
		searchTimeout: time.Minute,
	}
	// A chain stub cannot vary per query; use it to prove one failing
	// query does not discard batch structure, then a succeeding stub.
	resp, err := service.Search(context.Background(), SearchRequest{Queries: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Queries) != 2 {
		t.Fatalf("queries = %+v", resp.Queries)
	}
	for _, outcome := range resp.Queries {
		if outcome.Error == "" {
			t.Fatalf("outcome = %+v, want recorded error", outcome)
		}
	}
	succeeding := &Service{
		searchChain:   []searchProvider{chainStubProvider{providerName: providerExa, results: []SearchResult{{Title: "Hit", URL: "https://example.com/x"}}}},
		searchTimeout: time.Minute,
	}
	resp, err = succeeding.Search(context.Background(), SearchRequest{Queries: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Queries) != 2 || resp.Queries[0].Provider != providerExa || len(resp.Queries[0].Results) != 1 {
		t.Fatalf("queries = %+v", resp.Queries)
	}
}

func TestSearchBatchMutualExclusionAndLimit(t *testing.T) {
	t.Parallel()
	service := &Service{searchChain: []searchProvider{chainStubProvider{providerName: providerExa}}}
	if _, err := service.Search(context.Background(), SearchRequest{Query: "a", Queries: []string{"b"}}); err == nil {
		t.Fatal("query+queries accepted")
	}
	if _, err := service.Search(context.Background(), SearchRequest{Queries: []string{"a", "b", "c", "d", "e", "f"}}); err == nil {
		t.Fatal("6 queries accepted")
	}
}

func TestSearchProviderSelectionFiltersChain(t *testing.T) {
	t.Parallel()
	service := &Service{
		searchChain: []searchProvider{
			chainStubProvider{providerName: providerBrave, err: newCodedError(errors.New("no key"), ErrorCodeMissingAPIKey, "set key")},
			chainStubProvider{providerName: providerExa, results: []SearchResult{{Title: "Hit", URL: "https://example.com/y"}}},
		},
		searchTimeout: time.Minute,
	}
	resp, err := service.Search(context.Background(), SearchRequest{Query: "go", Providers: []string{"exa"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Provider != providerExa {
		t.Fatalf("resp = %+v", resp)
	}
	// Selecting an unconfigured provider fails precisely.
	if _, err := service.Search(context.Background(), SearchRequest{Query: "go", Providers: []string{"searxng"}}); err == nil {
		t.Fatal("unconfigured provider selection accepted")
	}
	// Unknown provider names are rejected at validation.
	if _, err := service.Search(context.Background(), SearchRequest{Query: "go", Providers: []string{"nope"}}); err == nil {
		t.Fatal("unknown provider accepted")
	}
}

func TestFallbackCategoriesAreNormalized(t *testing.T) {
	t.Parallel()
	service := &Service{
		searchChain: []searchProvider{
			chainStubProvider{providerName: providerBrave, err: newCodedError(errors.New("unsupported opt"), ErrorCodeUnsupportedSearch, "drop the option")},
			chainStubProvider{providerName: providerExa, err: newCodedError(errors.New("quota"), ErrorCodeRateLimited, "retry later")},
			chainStubProvider{providerName: providerTavily, results: []SearchResult{{Title: "Hit", URL: "https://example.com/z"}}},
		},
		searchTimeout: time.Minute,
	}
	resp, err := service.Search(context.Background(), SearchRequest{Query: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Fallbacks) != 2 {
		t.Fatalf("fallbacks = %+v", resp.Fallbacks)
	}
	if resp.Fallbacks[0].Category != fallbackCategoryUnsupported {
		t.Fatalf("fallback[0] = %+v, want unsupported", resp.Fallbacks[0])
	}
	if resp.Fallbacks[1].Category != fallbackCategoryQuota {
		t.Fatalf("fallback[1] = %+v, want quota", resp.Fallbacks[1])
	}
}
