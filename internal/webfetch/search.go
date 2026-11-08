package webfetch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type searchProvider interface {
	name() string
	search(context.Context, SearchRequest) ([]SearchResult, error)
}

type braveProvider struct {
	client   *Client
	endpoint string
	apiKey   string
}

func (p braveProvider) name() string { return providerBrave }

func (p braveProvider) search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, newCodedError(errors.New("search query is required"), ErrorCodeInvalidArgument, "provide a non-empty search query")
	}
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, newCodedError(errors.New("BRAVE_API_KEY is required for search"), ErrorCodeMissingAPIKey, "set BRAVE_API_KEY before using the search command")
	}
	if err := rejectBraveOptions(req); err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit == 0 {
		limit = DefaultSearchLimit
	}
	if limit < 1 || limit > MaxSearchLimit {
		return nil, newCodedError(fmt.Errorf("search limit must be between 1 and %d", MaxSearchLimit), ErrorCodeInvalidArgument, fmt.Sprintf("choose a limit from 1 to %d", MaxSearchLimit))
	}

	endpoint, err := url.Parse(p.endpoint)
	if err != nil {
		return nil, newCodedError(fmt.Errorf("parse search endpoint: %w", err), ErrorCodeInvalidArgument, "configure a valid search endpoint")
	}
	params := endpoint.Query()
	params.Set("q", braveSearchQuery(query, req.IncludeDomains))
	params.Set("count", strconv.Itoa(limit))
	endpoint.RawQuery = params.Encode()

	headers := make(http.Header)
	headers.Set("Accept", "application/json")
	headers.Set("X-Subscription-Token", strings.TrimSpace(p.apiKey))
	resp, err := p.client.Get(ctx, endpoint.String(), headers)
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
	}

	var payload braveSearchResponse
	if err := decodeJSON(resp.Body, &payload, "search"); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(payload.Web.Results))
	for _, result := range payload.Web.Results {
		results = append(results, SearchResult{
			Title:       result.Title,
			URL:         result.URL,
			Description: result.Description,
			Highlights:  braveHighlights(req.IncludeHighlights, result.Description),
		})
	}
	return results, nil
}

func rejectBraveOptions(req SearchRequest) error {
	switch {
	case strings.TrimSpace(req.Category) != "":
		return unsupportedSearchOptionError("category")
	case strings.TrimSpace(req.StartPublishedDate) != "":
		return unsupportedSearchOptionError("start_published_date")
	default:
		return nil
	}
}

func braveSearchQuery(query string, domains []string) string {
	if len(domains) == 0 {
		return query
	}
	scopes := make([]string, 0, len(domains))
	for _, domain := range domains {
		scopes = append(scopes, "site:"+domain)
	}
	if len(scopes) == 1 {
		return scopes[0] + " " + query
	}
	return "(" + strings.Join(scopes, " OR ") + ") " + query
}

func braveHighlights(include bool, description string) []string {
	if !include || strings.TrimSpace(description) == "" {
		return nil
	}
	return []string{description}
}

type braveSearchResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

// splitProviderNames parses the ordered comma-separated provider list.
// Empty entries are dropped; an empty list means the default provider.
func splitProviderNames(list string) []string {
	if strings.TrimSpace(list) == "" {
		return nil
	}
	parts := strings.Split(list, ",")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		if name := strings.ToLower(strings.TrimSpace(part)); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// Search executes one normalized query against the configured provider
// chain in order. A provider that errors or returns zero results falls
// through to the next with a recorded reason (error code, "empty", or
// "domain-miss" when domain filters were set); the whole routing pass is
// bounded by the configured service timeout.
const (
	fallbackReasonEmpty      = "empty"
	fallbackReasonDomainMiss = "domain-miss"
)

// Search runs one normalized query (or a bounded batch) against the
// configured provider chain.
//
//nolint:gocognit,gocyclo,revive // branchy by design; splitting would scatter one sequential algorithm
func (s *Service) Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	if s == nil {
		return SearchResponse{}, newCodedError(errors.New("webfetch service is nil"), ErrorCodeInvalidArgument, "initialize the webfetch service before calling Search")
	}
	if s.searchConfig != nil {
		return SearchResponse{}, s.searchConfig
	}
	validated, err := validateSearchRequest(req, DefaultSearchLimit)
	if err != nil {
		return SearchResponse{}, err
	}
	chain := s.searchChain
	if len(chain) == 0 {
		if s.search == nil {
			return SearchResponse{}, newCodedError(errors.New("search provider is not configured"), ErrorCodeInvalidArgument, "configure a search provider before calling Search")
		}
		chain = []searchProvider{s.search}
	}
	if len(validated.Providers) > 0 {
		filtered, filterErr := filterSearchChain(chain, validated.Providers)
		if filterErr != nil {
			return SearchResponse{}, filterErr
		}
		chain = filtered
	}
	if s.searchTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.searchTimeout)
		defer cancel()
	}
	if len(validated.Queries) > 0 {
		return s.searchBatch(ctx, chain, validated), nil
	}
	resp, err := s.searchOne(ctx, chain, validated)
	if err != nil {
		return SearchResponse{}, err
	}
	if validated.Workflow == SearchWorkflowExtractiveSummary {
		resp.Summary = extractiveSummary(resp.Results)
	}
	return resp, nil
}

// searchBatch runs each query sequentially with independent outcomes: one
// failed query never discards the others.
func (s *Service) searchBatch(ctx context.Context, chain []searchProvider, req SearchRequest) SearchResponse {
	outcomes := make([]SearchQueryOutcome, 0, len(req.Queries))
	for _, query := range req.Queries {
		single := req
		single.Query = query
		single.Queries = nil
		resp, searchErr := s.searchOne(ctx, chain, single)
		outcome := SearchQueryOutcome{Query: query}
		if searchErr != nil {
			outcome.Error = searchErr.Error()
		} else {
			outcome.Provider = resp.Provider
			outcome.Results = resp.Results
			outcome.Fallbacks = resp.Fallbacks
			if req.Workflow == SearchWorkflowExtractiveSummary {
				outcome.Summary = extractiveSummary(resp.Results)
			}
		}
		outcomes = append(outcomes, outcome)
		if ctx.Err() != nil {
			break
		}
	}
	return SearchResponse{Queries: outcomes}
}

// filterSearchChain restricts the configured chain to an explicit ordered
// provider selection. Selected providers must be configured; selecting an
// unconfigured provider fails precisely instead of silently widening.
func filterSearchChain(chain []searchProvider, names []string) ([]searchProvider, error) {
	byName := make(map[string]searchProvider, len(chain))
	for _, provider := range chain {
		byName[provider.name()] = provider
	}
	filtered := make([]searchProvider, 0, len(names))
	for _, name := range names {
		provider, ok := byName[name]
		if !ok {
			return nil, newCodedError(fmt.Errorf("search provider %q is not configured", name), ErrorCodeInvalidArgument, "configure the selected provider or remove it from the selection")
		}
		filtered = append(filtered, provider)
	}
	return filtered, nil
}

// searchOne executes one normalized query against the ordered chain. A
// provider that errors or returns zero results falls through to the next
// with a recorded reason and normalized routing category; the last
// provider's error or empty result is returned as-is.
//
//nolint:gocognit,gocyclo,revive // branchy by design; splitting would scatter one sequential algorithm
func (s *Service) searchOne(ctx context.Context, chain []searchProvider, validated SearchRequest) (SearchResponse, error) {
	if validated.Combine && len(chain) > 1 {
		return s.combineSearch(ctx, chain, validated)
	}
	var fallbacks []SearchFallback
	for i, provider := range chain {
		results, searchErr := provider.search(ctx, validated)
		last := i == len(chain)-1
		switch {
		case searchErr == nil && len(results) > 0:
			return SearchResponse{
				Query:     validated.Query,
				Provider:  provider.name(),
				Results:   normalizeSearchResults(results, validated.Limit),
				Fallbacks: fallbacks,
			}, nil
		case searchErr != nil:
			if last {
				return SearchResponse{}, searchErr
			}
			fallbacks = append(fallbacks, SearchFallback{Provider: provider.name(), Reason: searchErrorReason(searchErr), Category: searchFallbackCategory(searchErr)})
		default: // empty results
			if last {
				return SearchResponse{Query: validated.Query, Provider: provider.name(), Results: []SearchResult{}, Fallbacks: fallbacks}, nil
			}
			reason := "empty"
			if len(validated.IncludeDomains) > 0 {
				reason = "domain-miss"
			}
			fallbacks = append(fallbacks, SearchFallback{Provider: provider.name(), Reason: reason, Category: "empty"})
		}
	}
	return SearchResponse{}, newCodedError(errors.New("search provider is not configured"), ErrorCodeInvalidArgument, "configure a search provider before calling Search")
}

// Routing categories (slice 2) for fallback entries.
const (
	fallbackCategoryUnsupported     = "unsupported"
	fallbackCategoryTransient       = "transient"
	fallbackCategoryQuota           = "quota"
	fallbackCategoryNetwork         = "network"
	fallbackCategoryInvalidResponse = "invalid_response"
	fallbackCategoryEmpty           = "empty"
)

// searchFallbackCategory maps a provider error to its normalized routing
// category. Successful empty results (handled inline) use "empty".
func searchFallbackCategory(err error) string {
	var coded *CodedError
	if errors.As(err, &coded) {
		switch coded.Code {
		case ErrorCodeUnsupportedSearch:
			return fallbackCategoryUnsupported
		case ErrorCodeRateLimited:
			return fallbackCategoryQuota
		case ErrorCodeProviderResponse, ErrorCodeInvalidJSON:
			return fallbackCategoryInvalidResponse
		case ErrorCodeRequestTimeout:
			return fallbackCategoryTransient
		case ErrorCodeInvalidArgument:
			return fallbackCategoryUnsupported
		}
	}
	message := strings.ToLower(err.Error())
	for _, networkHint := range []string{"connection refused", "no such host", "dial tcp", "network is unreachable", "connection reset", "eof", "tls handshake", "server misbehaving"} {
		if strings.Contains(message, networkHint) {
			return fallbackCategoryNetwork
		}
	}
	return fallbackCategoryTransient
}

// normalizeSearchQueries trims batch entries, dropping none (empty entries
// are validation errors, not silently discarded).
func normalizeSearchQueries(queries []string) []string {
	if len(queries) == 0 {
		return nil
	}
	normalized := make([]string, len(queries))
	for i, query := range queries {
		normalized[i] = strings.TrimSpace(query)
	}
	return normalized
}

// normalizeProviderNames lowercases and deduplicates an explicit provider
// selection, preserving order.
func normalizeProviderNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(names))
	normalized := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		normalized = append(normalized, name)
	}
	return normalized
}

// searchErrorReason reduces a provider error to its stable code, or "error".
func searchErrorReason(err error) string {
	var coded *CodedError
	if errors.As(err, &coded) && coded.Code != "" {
		return coded.Code
	}
	return "error"
}

// validateSearchQueries checks the single/batch query shape.
func validateSearchQueries(req *SearchRequest) error {
	if req.Query != "" && len(req.Queries) > 0 {
		return newCodedError(errors.New("search: 'query' and 'queries' are mutually exclusive"), ErrorCodeInvalidArgument, "pass either a single query or a queries batch, not both")
	}
	if len(req.Queries) > MaxSearchQueries {
		return newCodedError(fmt.Errorf("search queries may contain at most %d entries", MaxSearchQueries), ErrorCodeInvalidArgument, "split the batch across calls")
	}
	if len(req.Queries) == 0 && req.Query == "" {
		return newCodedError(errors.New("search query is required"), ErrorCodeInvalidArgument, "provide a non-empty search query")
	}
	for _, query := range req.Queries {
		if query == "" {
			return newCodedError(errors.New("search queries must not contain empty entries"), ErrorCodeInvalidArgument, "provide non-empty search queries")
		}
		if len(query) > 4096 {
			return newCodedError(errors.New("search query must be at most 4096 characters"), ErrorCodeInvalidArgument, "shorten the query before retrying")
		}
	}
	if len(req.Query) > 4096 {
		return newCodedError(errors.New("search query must be at most 4096 characters"), ErrorCodeInvalidArgument, "shorten the query before retrying")
	}
	return nil
}

// validateSearchProviders normalizes and checks an explicit provider selection.
func validateSearchProviders(req *SearchRequest) error {
	req.Providers = normalizeProviderNames(req.Providers)
	for _, name := range req.Providers {
		if _, ok := searchProviderRegistry[name]; !ok {
			return unsupportedSearchProviderError(name)
		}
	}
	return nil
}

// validateSearchWorkflow normalizes and checks the search workflow.
func validateSearchWorkflow(req *SearchRequest) error {
	req.Workflow = strings.TrimSpace(req.Workflow)
	if req.Workflow == "" {
		req.Workflow = SearchWorkflowResults
	}
	if req.Workflow != SearchWorkflowResults && req.Workflow != SearchWorkflowExtractiveSummary {
		return newCodedError(fmt.Errorf("unsupported search workflow %q", req.Workflow), ErrorCodeInvalidArgument, "use workflow results or extractive_summary")
	}
	return nil
}

//nolint:gocyclo // Sequential validation preserves precise field-specific errors.
func validateSearchRequest(req SearchRequest, defaultLimit int) (SearchRequest, error) {
	req.Query = strings.TrimSpace(req.Query)
	req.Queries = normalizeSearchQueries(req.Queries)
	if err := validateSearchQueries(&req); err != nil {
		return SearchRequest{}, err
	}
	if err := validateSearchProviders(&req); err != nil {
		return SearchRequest{}, err
	}
	if err := validateSearchWorkflow(&req); err != nil {
		return SearchRequest{}, err
	}
	if req.Limit == 0 {
		req.Limit = defaultLimit
	}
	if req.Limit < 1 || req.Limit > MaxSearchLimit {
		return SearchRequest{}, newCodedError(fmt.Errorf("search limit must be between 1 and %d", MaxSearchLimit), ErrorCodeInvalidArgument, fmt.Sprintf("choose a limit from 1 to %d", MaxSearchLimit))
	}
	req.Category = strings.TrimSpace(req.Category)
	if len(req.Category) > 100 {
		return SearchRequest{}, newCodedError(errors.New("search category must be at most 100 characters"), ErrorCodeInvalidArgument, "shorten the category before retrying")
	}
	req.IncludeDomains = append([]string(nil), req.IncludeDomains...)
	for i := range req.IncludeDomains {
		req.IncludeDomains[i] = strings.TrimSpace(req.IncludeDomains[i])
		if !validSearchHostname(req.IncludeDomains[i]) {
			return SearchRequest{}, newCodedError(fmt.Errorf("invalid search domain at index %d", i), ErrorCodeInvalidArgument, "use hostnames such as example.com without a scheme or path")
		}
	}
	if len(req.IncludeDomains) > 50 {
		return SearchRequest{}, newCodedError(errors.New("search domain filters may contain at most 50 domains"), ErrorCodeInvalidArgument, "reduce the domain filter list before retrying")
	}
	if req.StartPublishedDate != "" {
		normalized, err := normalizeSearchDate(req.StartPublishedDate)
		if err != nil {
			return SearchRequest{}, err
		}
		req.StartPublishedDate = normalized
	}
	if req.IncludeHighlights && req.HighlightSentences == 0 {
		req.HighlightSentences = 3
	}
	if req.HighlightSentences < 0 || req.HighlightSentences > 10 || (req.IncludeHighlights && req.HighlightSentences < 1) {
		return SearchRequest{}, newCodedError(errors.New("highlight sentences must be between 1 and 10"), ErrorCodeInvalidArgument, "choose a highlight sentence count from 1 through 10")
	}
	return req, nil
}

// validSearchHostname keeps domain filters from introducing Brave query operators.
func validSearchHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for i := 0; i < len(label); i++ {
			edge := i == 0 || i == len(label)-1
			if !asciiAlnum(label[i]) && (edge || label[i] != '-') {
				return false
			}
		}
	}
	return true
}

func asciiAlnum(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
}

func normalizeSearchResults(results []SearchResult, limit int) []SearchResult {
	seen := make(map[string]struct{}, len(results))
	normalized := make([]SearchResult, 0, min(len(results), limit))
	for _, result := range results {
		result.Title = strings.TrimSpace(result.Title)
		result.URL = strings.TrimSpace(result.URL)
		result.Description = strings.TrimSpace(result.Description)
		result.PublishedAt = strings.TrimSpace(result.PublishedAt)
		result.Highlights = cleanSearchStrings(result.Highlights)
		if result.URL == "" {
			continue
		}
		key := canonicalSearchURL(result.URL)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, result)
		if len(normalized) == limit {
			break
		}
	}
	return normalized
}

func cleanSearchStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func canonicalSearchURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	return parsed.String()
}

func unsupportedSearchProviderError(provider string) error {
	return newCodedError(fmt.Errorf("unsupported search provider %q", provider), ErrorCodeInvalidArgument, "choose search provider brave, exa, or searxng")
}

func unsupportedSearchOptionError(option string) error {
	return newCodedError(fmt.Errorf("search provider brave does not support %s", option), ErrorCodeUnsupportedSearch, "select the exa search provider for this option")
}
