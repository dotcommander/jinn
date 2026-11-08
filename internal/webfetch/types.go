package webfetch

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

//nolint:revive // Grouped exported defaults form one bounded service policy.
const (
	DefaultTimeout               = 30 * time.Second
	DefaultMaxBodyBytes          = 8 << 20
	DefaultMaxImageBytes         = 20 << 20
	DefaultRenderTimeout         = 30 * time.Second
	DefaultRenderMaxConcurrency  = 2
	DefaultRenderMaxRequests     = 100
	DefaultRenderMaxNetworkBytes = 32 << 20
	DefaultSearchLimit           = 5
	MaxSearchLimit               = 50
	DefaultReaderEndpoint        = "https://r.jina.ai"
	DefaultSearchEndpoint        = "https://api.search.brave.com/res/v1/web/search"
	DefaultExaEndpoint           = "https://api.exa.ai/search"
	DefaultTavilyEndpoint        = "https://api.tavily.com/search"
	DefaultSearXNGEndpoint       = "http://localhost:8080/search"
	DefaultUserAgent             = "webfetch/0.1"
	// Snapshot store defaults (slice 1): durable, opt-in result snapshots.
	DefaultSnapshotTTL           = 24 * time.Hour
	DefaultSnapshotMaxEntries    = 50
	DefaultSnapshotMaxBytes      = 64 << 20
	DefaultSnapshotMaxEntryBytes = 8 << 20
	// MaxSearchQueries bounds one batched web_search call (slice 2).
	MaxSearchQueries = 5
)

const (
	schemeHTTP                 = "http"
	schemeHTTPS                = "https"
	headerAuthorization        = "Authorization"
	headerSubscription         = "X-Subscription-Token"
	headerAPIKey               = "x-api-key"
	contentTypeMarkdown        = "text/markdown"
	providerBrave              = "brave"
	providerExa                = "exa"
	providerTavily             = "tavily"
	providerSearXNG            = "searxng"
	sourceReader               = "reader"
	sourceImage                = "image"
	truncatedByLines           = "lines"
	truncatedByBytes           = "bytes"
	webrtcDisableNonProxiedUDP = "disable_non_proxied_udp"
)

// Config controls provider endpoints and HTTP safety limits.
// SearchProvider may name several providers as an ordered comma-separated
// list (e.g. "brave,exa"): Search tries them in order, falling back on
// error or empty results and reporting each fallback reason.
type Config struct {
	// UserAgent identifies the owning application for direct and rendered HTTP requests.
	UserAgent             string
	JinaAPIKey            string
	BraveAPIKey           string
	ExaAPIKey             string
	TavilyAPIKey          string
	ReaderMode            string
	ReaderEndpoint        string
	SearchEndpoint        string
	ExaSearchEndpoint     string
	TavilySearchEndpoint  string
	SearXNGSearchEndpoint string
	SearXNGAPIKey         string
	SearchProvider        string
	URLCacheTTL           time.Duration
	URLCacheDir           string
	// SnapshotDir overrides the durable snapshot store directory; empty
	// derives a sibling of URLCacheDir (or the default cache home).
	SnapshotDir           string
	SnapshotTTL           time.Duration
	Timeout               time.Duration
	MaxBodyBytes          int64
	AllowPrivateNetworks  bool
	RenderTimeout         time.Duration
	RenderMaxConcurrency  int
	RenderMaxRequests     int
	RenderMaxNetworkBytes int64
	RenderMaxHTMLBytes    int64
	ChromePath            string
	GitHubAPIEndpoint     string
	GitHubToken           string
}

// ClientConfig controls the shared HTTP client.
type ClientConfig struct {
	Timeout              time.Duration
	MaxBodyBytes         int64
	AllowPrivateNetworks bool
	UserAgent            string
}

// FetchRequest describes one URL fetch.
type FetchRequest struct {
	URL        string
	Raw        bool
	Reader     string
	Render     string
	RenderWait string
}

// Document is the normalized result of a URL fetch.
type Document struct {
	URL         string
	FinalURL    string
	StatusCode  int
	ContentType string
	Title       string
	Description string
	Domain      string
	Favicon     string
	Image       string
	Language    string
	Published   string
	Author      string
	Site        string
	WordCount   int
	Extractor   string
	Source      string
	Rendered    bool
	Warnings    []string
	// Quality is the extraction-quality signal ("content" or "chrome_only")
	// computed from the extracted body at assembly time.
	Quality string
	// Outcome is the standardized fetch-outcome taxonomy (slice 5):
	// content, suspected_challenge, empty_extraction, or unsupported_media
	// on success paths; auth/rate-limit map from coded error codes.
	Outcome       string
	Content       string
	ImageBytes    []byte
	ImageMimeType string
}

// SearchRequest describes one web search.
type SearchRequest struct {
	Query string
	// Queries, when non-empty, replaces Query with a bounded batch
	// (max MaxSearchQueries). Query and Queries are mutually exclusive.
	Queries []string
	// Providers, when non-empty, restricts the search to an explicit
	// ordered subset of the configured provider chain (slice 2).
	Providers []string
	// Workflow selects "results" (default) or "extractive_summary"
	// (slice 4): a deterministic, cited digest alongside the results.
	Workflow           string
	Limit              int
	Category           string
	IncludeDomains     []string
	StartPublishedDate string
	IncludeHighlights  bool
	HighlightSentences int
	// Combine queries up to targetUsable backends in parallel and merges
	// their results by Reciprocal Rank Fusion instead of returning the
	// first usable backend's ranking (opt-in: it costs one quota unit per
	// backend it touches).
	Combine bool
}

// Search workflow values accepted by web_search.
const (
	SearchWorkflowResults           = "results"
	SearchWorkflowExtractiveSummary = "extractive_summary"
)

// SearchSummary is the deterministic extractive digest (slice 4). It
// selects source-provided snippets with result citations; it never calls
// a model.
type SearchSummary struct {
	Status    string `json:"status"` // "ok" or "insufficient_evidence"
	Text      string `json:"text"`
	Citations []int  `json:"citations,omitempty"` // 1-based result indexes
}

// SearchResponse is the normalized search result.
type SearchResponse struct {
	Query    string         `json:"query"`
	Provider string         `json:"provider,omitempty"`
	Results  []SearchResult `json:"results"`
	// Fallbacks records each provider skipped before the answering one,
	// with the reason (error code, "empty", or "domain-miss") and the
	// normalized routing category (unsupported, transient, quota,
	// network, invalid_response, or empty).
	Fallbacks []SearchFallback `json:"fallbacks,omitempty"`
	// Combine reports whether the results were fused from several backends.
	Combine bool `json:"combine,omitempty"`
	// Queries holds per-query outcomes for batched searches. When set,
	// Query/Results stay empty and each entry is independent: one failed
	// query never discards the others.
	Queries []SearchQueryOutcome `json:"queries,omitempty"`
	// Summary is the extractive digest when Workflow is extractive_summary.
	Summary *SearchSummary `json:"summary,omitempty"`
}

// SearchQueryOutcome is one query result inside a batched search.
type SearchQueryOutcome struct {
	Query     string           `json:"query"`
	Provider  string           `json:"provider,omitempty"`
	Results   []SearchResult   `json:"results"`
	Fallbacks []SearchFallback `json:"fallbacks,omitempty"`
	Error     string           `json:"error,omitempty"`
	// Summary is the extractive digest for this query when requested.
	Summary *SearchSummary `json:"summary,omitempty"`
}

// SearchFallback records one provider that did not answer a search.
type SearchFallback struct {
	Provider string `json:"provider"`
	Reason   string `json:"reason"`
	// Category is the normalized routing category for this fallback.
	Category string `json:"category,omitempty"`
}

// SearchResult is one search hit.
type SearchResult struct {
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Description string   `json:"description,omitempty"`
	PublishedAt string   `json:"published_at,omitempty"`
	Highlights  []string `json:"highlights,omitempty"`
	// Sources lists the backends that returned this hit (combine mode).
	Sources []string `json:"sources,omitempty"`
}

type httpResponse struct {
	URL        string
	StatusCode int
	Header     http.Header
	Body       []byte
}

// HTTPError represents a bounded non-success HTTP response.
type HTTPError struct {
	URL        string
	StatusCode int
	Status     string
	Body       string
}

func (e *HTTPError) Error() string {
	if strings.TrimSpace(e.Body) == "" {
		return fmt.Sprintf("%s: %s", e.URL, e.Status)
	}
	return fmt.Sprintf("%s: %s: %s", e.URL, e.Status, e.Body)
}
