package webfetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type tavilyProvider struct {
	client   *Client
	endpoint string
	apiKey   string
}

func (p tavilyProvider) name() string { return providerTavily }

type tavilySearchRequest struct {
	APIKey         string   `json:"api_key"`
	Query          string   `json:"query"`
	MaxResults     int      `json:"max_results"`
	IncludeDomains []string `json:"include_domains,omitempty"`
	StartDate      string   `json:"start_date,omitempty"`
	IncludeAnswer  bool     `json:"include_answer"`
}

type tavilySearchResponse struct {
	Results []tavilyResult `json:"results"`
}

type tavilyResult struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	Content       string `json:"content"`
	PublishedDate string `json:"published_date"`
}

func (p tavilyProvider) search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, newCodedError(errors.New("TAVILY_API_KEY is required for Tavily search"), ErrorCodeMissingAPIKey, "set TAVILY_API_KEY before selecting the tavily search provider")
	}
	if strings.TrimSpace(req.Category) != "" {
		return nil, unsupportedSearchOptionError("category")
	}
	body := tavilySearchRequest{
		APIKey:         strings.TrimSpace(p.apiKey),
		Query:          req.Query,
		MaxResults:     req.Limit,
		IncludeDomains: append([]string(nil), req.IncludeDomains...),
	}
	if req.StartPublishedDate != "" {
		start, err := time.Parse(time.RFC3339, req.StartPublishedDate)
		if err != nil {
			return nil, newCodedError(fmt.Errorf("tavily start_date: %w", err), ErrorCodeInvalidArgument, "use a valid RFC3339 or YYYY-MM-DD start date")
		}
		body.StartDate = start.UTC().Format("2006-01-02")
	}
	payload, err := json.Marshal(body) //nolint:gosec // Tavily requires the API key in the request body by provider contract; it is never logged.
	if err != nil {
		return nil, fmt.Errorf("marshal Tavily request: %w", err)
	}
	headers := make(http.Header)
	headers.Set("Accept", "application/json")
	headers.Set("Content-Type", "application/json")
	resp, err := p.client.Do(ctx, http.MethodPost, p.endpoint, headers, payload)
	if err != nil {
		return nil, fmt.Errorf("tavily search request: %w", err)
	}
	var decoded tavilySearchResponse
	if err := decodeJSON(resp.Body, &decoded, "Tavily search"); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(decoded.Results))
	for _, result := range decoded.Results {
		highlight := strings.TrimSpace(result.Content)
		var highlights []string
		if req.IncludeHighlights && highlight != "" {
			highlights = []string{highlight}
		}
		results = append(results, SearchResult{
			Title:       result.Title,
			URL:         result.URL,
			Description: highlight,
			PublishedAt: result.PublishedDate,
			Highlights:  highlights,
		})
	}
	return results, nil
}
