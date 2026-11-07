package webfetch

import (
	"context"
	"errors"
	"testing"
)

type combineStubProvider struct {
	backend string
	results []SearchResult
	err     error
}

func (p combineStubProvider) name() string { return p.backend }
func (p combineStubProvider) search(context.Context, SearchRequest) ([]SearchResult, error) {
	return p.results, p.err
}

func combineService(chain ...searchProvider) *Service {
	return &Service{searchChain: chain, search: nil}
}

func TestReciprocalRankFusionOrdersCorroboratedHits(t *testing.T) {
	t.Parallel()
	batches := []backendCombineResults{
		{backend: "a", results: []SearchResult{
			{URL: "https://x.example/", Description: "x"},
			{URL: "https://y.example/", Description: "y-a"},
			{URL: "https://z.example/", Description: "z"},
		}},
		{backend: "b", results: []SearchResult{
			{URL: "https://y.example/", Description: "y-b-with-more-text"},
			{URL: "https://w.example/", Description: "w"},
		}},
	}
	merged := reciprocalRankFusion(batches, 10)
	if len(merged) != 4 {
		t.Fatalf("merged = %v", merged)
	}
	// y: 1/62 + 1/61 > x: 1/61 > z: 1/63 > w: 1/62? order: y, x, z, w or y, x, w, z
	if merged[0].URL != "https://y.example/" {
		t.Fatalf("first = %q, want corroborated y (1/62+1/61)", merged[0].URL)
	}
	if len(merged[0].Sources) != 2 {
		t.Fatalf("sources = %v, want both backends", merged[0].Sources)
	}
	if merged[0].Description != "y-b-with-more-text" {
		t.Fatalf("richest result not kept: %q", merged[0].Description)
	}
}

func TestCombineSearchUsesTargetedBatching(t *testing.T) {
	t.Parallel()
	service := combineService(
		combineStubProvider{backend: "a", results: []SearchResult{{URL: "https://a.example/"}}},
		combineStubProvider{backend: "b", err: errors.New("boom")},
		combineStubProvider{backend: "c", results: []SearchResult{{URL: "https://c.example/"}}},
		combineStubProvider{backend: "d", results: []SearchResult{{URL: "https://d.example/"}}},
	)
	resp, err := service.combineSearch(context.Background(), service.searchChain, SearchRequest{Query: "q", Limit: 6, Combine: true})
	if err != nil {
		t.Fatalf("combineSearch: %v", err)
	}
	if !resp.Combine {
		t.Fatal("Combine = false")
	}
	// batch1 = a,b,c (target 3); b fails -> 2 usable; batch2 = d -> 3 usable; stop.
	if len(resp.Results) != 3 {
		t.Fatalf("results = %v", resp.Results)
	}
	if len(resp.Fallbacks) != 1 {
		t.Fatalf("fallbacks = %v", resp.Fallbacks)
	}
}

func TestCombineSearchAllFail(t *testing.T) {
	t.Parallel()
	service := combineService(
		combineStubProvider{backend: "a", err: errors.New("down")},
		combineStubProvider{backend: "b", results: nil},
	)
	_, err := service.combineSearch(context.Background(), service.searchChain, SearchRequest{Query: "q", Limit: 5, Combine: true})
	if err == nil {
		t.Fatal("expected error when every backend fails")
	}
}
