package webfetch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSearXNGSearch(t *testing.T) {
	t.Parallel()
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"title": "Example", "url": "https://example.com/a", "content": "first snippet"},
				{"title": "Second", "url": "https://example.com/b", "content": "second snippet"},
			},
		})
	}))
	t.Cleanup(server.Close)

	service := NewService(Config{
		AllowPrivateNetworks:  true,
		SearchProvider:        providerSearXNG,
		SearXNGSearchEndpoint: server.URL,
	})
	resp, err := service.Search(context.Background(), SearchRequest{Query: "web search", Limit: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.Provider != providerSearXNG {
		t.Fatalf("provider = %q", resp.Provider)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results = %d, want 1 (limit)", len(resp.Results))
	}
	if resp.Results[0].Title != "Example" || resp.Results[0].Description != "first snippet" {
		t.Fatalf("result = %+v", resp.Results[0])
	}
	if gotQuery.Get("format") != "json" || gotQuery.Get("q") != "web search" {
		t.Fatalf("query = %v", gotQuery)
	}
}

func TestSearXNGForbiddenHint(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(server.Close)

	service := NewService(Config{
		AllowPrivateNetworks:  true,
		SearchProvider:        providerSearXNG,
		SearXNGSearchEndpoint: server.URL,
	})
	_, err := service.Search(context.Background(), SearchRequest{Query: "x"})
	if err == nil {
		t.Fatal("Search succeeded, want 403 error")
	}
	if !strings.Contains(err.Error(), "403") && !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("error = %v, want 403 surface", err)
	}
}
