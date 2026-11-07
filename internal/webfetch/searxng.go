package webfetch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// searxngProvider is the keyless self-hosted search backend. It queries a
// user-run SearXNG instance's JSON search API (GET /search?format=json);
// the instance must have "json" enabled under search.formats in its
// settings.yml. An optional Bearer key authenticates reverse-proxied
// instances; without one the provider is still usable.
type searxngProvider struct {
	client   *Client
	endpoint string
	apiKey   string
}

func (p searxngProvider) name() string { return providerSearXNG }

type searxngResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

type searxngResponse struct {
	Results []searxngResult `json:"results"`
}

func (p searxngProvider) search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, newCodedError(errors.New("search query is required"), ErrorCodeInvalidArgument, "provide a non-empty search query")
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
	params.Set("format", "json")
	params.Set("safesearch", "0")
	endpoint.RawQuery = params.Encode()

	headers := make(http.Header)
	headers.Set("Accept", "application/json")
	if strings.TrimSpace(p.apiKey) != "" {
		headers.Set(headerAuthorization, "Bearer "+strings.TrimSpace(p.apiKey))
	}
	resp, err := p.client.Get(ctx, endpoint.String(), headers)
	if err != nil {
		return nil, searxngSearchError(err)
	}

	var payload searxngResponse
	if err := decodeJSON(resp.Body, &payload, "SearXNG search"); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, min(len(payload.Results), limit))
	for _, result := range payload.Results {
		if len(results) == limit {
			break
		}
		results = append(results, SearchResult{
			Title:       result.Title,
			URL:         result.URL,
			Description: result.Content,
			Highlights:  braveHighlights(req.IncludeHighlights, result.Content),
		})
	}
	return results, nil
}

// searxngSearchError maps SearXNG's two common non-2xx responses to
// actionable suggestions: 401 means reverse-proxy auth rejected the Bearer
// key; 403 from a default instance almost always means JSON output is
// disabled. Other errors pass through unchanged.
func searxngSearchError(err error) error {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		return fmt.Errorf("SearXNG search request: %w", err)
	}
	switch httpErr.StatusCode {
	case http.StatusUnauthorized:
		return newCodedError(httpErr, ErrorCodeAuthentication, "check the SearXNG reverse-proxy auth or SEARXNG_API_KEY")
	case http.StatusForbidden:
		return newCodedError(httpErr, ErrorCodeUpstreamHTTP, "enable 'json' under search.formats in the SearXNG settings.yml")
	default:
		return fmt.Errorf("SearXNG search request: %w", err)
	}
}
