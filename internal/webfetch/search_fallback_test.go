package webfetch

import (
	"context"
	"errors"
	"testing"
	"time"
)

type chainStubProvider struct {
	providerName string
	results      []SearchResult
	err          error
}

func (p chainStubProvider) name() string { return p.providerName }

func (p chainStubProvider) search(context.Context, SearchRequest) ([]SearchResult, error) {
	return p.results, p.err
}

// Discriminating check (pi-web-access adoption): a first provider returning
// zero results must trigger the next provider, with the fallback reason
// reported — not an empty result returned.
func TestSearchFallsBackOnEmpty(t *testing.T) {
	t.Parallel()
	service := &Service{
		searchChain: []searchProvider{
			chainStubProvider{providerName: providerBrave, results: nil},
			chainStubProvider{providerName: providerExa, results: []SearchResult{{Title: "Hit", URL: "https://example.com/a"}}},
		},
		searchTimeout: time.Minute,
	}
	response, err := service.Search(context.Background(), SearchRequest{Query: "go", IncludeDomains: []string{"example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Provider != providerExa || len(response.Results) != 1 {
		t.Fatalf("response = %+v", response)
	}
	if len(response.Fallbacks) != 1 || response.Fallbacks[0].Provider != providerBrave || response.Fallbacks[0].Reason != "domain-miss" {
		t.Fatalf("fallbacks = %+v", response.Fallbacks)
	}
}

func TestSearchFallsBackOnError(t *testing.T) {
	t.Parallel()
	service := &Service{
		searchChain: []searchProvider{
			chainStubProvider{providerName: providerBrave, err: newCodedError(errors.New("quota"), ErrorCodeRateLimited, "retry later")},
			chainStubProvider{providerName: providerExa, results: []SearchResult{{Title: "Hit", URL: "https://example.com/b"}}},
		},
		searchTimeout: time.Minute,
	}
	response, err := service.Search(context.Background(), SearchRequest{Query: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Provider != providerExa {
		t.Fatalf("response = %+v", response)
	}
	if len(response.Fallbacks) != 1 || response.Fallbacks[0].Reason != ErrorCodeRateLimited {
		t.Fatalf("fallbacks = %+v", response.Fallbacks)
	}
}

func TestSearchSingleProviderEmptyIsNotAnError(t *testing.T) {
	t.Parallel()
	service := &Service{
		searchChain:   []searchProvider{chainStubProvider{providerName: providerBrave}},
		searchTimeout: time.Minute,
	}
	response, err := service.Search(context.Background(), SearchRequest{Query: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Provider != providerBrave || len(response.Results) != 0 || len(response.Fallbacks) != 0 {
		t.Fatalf("response = %+v", response)
	}
}
