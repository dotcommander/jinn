package webfetch

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestServiceFetchRaw(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Error("missing User-Agent")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	service := NewService(Config{AllowPrivateNetworks: true})
	doc, err := service.Fetch(context.Background(), FetchRequest{URL: server.URL, Raw: true})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if doc.Source != "raw" || doc.Content != `{"ok":true}` || doc.StatusCode != http.StatusOK {
		t.Fatalf("document = %+v", doc)
	}
	if !strings.HasPrefix(doc.ContentType, "application/json") {
		t.Fatalf("content type = %q", doc.ContentType)
	}
}

//nolint:gocyclo // One provider contract test validates request and response mapping end to end.
func TestServiceSearchExaProviderSerializesRichRequest(t *testing.T) {
	t.Parallel()
	var gotRequest exaSearchRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if got := r.Header.Get("x-api-key"); got != "exa-secret" {
			t.Errorf("x-api-key = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":" Exa result ","url":"https://example.com/article","publishedDate":"2026-08-01T00:00:00Z","highlights":[" Highlight "]}]}`))
	}))
	t.Cleanup(server.Close)

	service := NewService(Config{
		AllowPrivateNetworks: true,
		SearchProvider:       "exa",
		ExaAPIKey:            "exa-secret",
		ExaSearchEndpoint:    server.URL,
	})
	result, err := service.Search(context.Background(), SearchRequest{
		Query:              "go",
		Limit:              5,
		Category:           "news",
		IncludeDomains:     []string{"go.dev"},
		StartPublishedDate: "2026-08-01",
		IncludeHighlights:  true,
		HighlightSentences: 3,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotRequest.Query != "go" || gotRequest.NumResults != 5 || gotRequest.Category != "news" {
		t.Fatalf("request = %+v", gotRequest)
	}
	if gotRequest.StartPublishedDate != "2026-08-01T00:00:00Z" || len(gotRequest.IncludeDomains) != 1 || gotRequest.Contents == nil {
		t.Fatalf("rich request = %+v", gotRequest)
	}
	if gotRequest.Contents.Highlights.NumSentences != 3 {
		t.Fatalf("highlight request = %+v", gotRequest.Contents)
	}
	if result.Provider != "exa" || len(result.Results) != 1 || result.Results[0].Title != "Exa result" || result.Results[0].PublishedAt == "" || len(result.Results[0].Highlights) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestServiceSearchExaRequiresAPIKey(t *testing.T) {
	service := NewService(Config{SearchProvider: "exa"})
	_, err := service.Search(context.Background(), SearchRequest{Query: "go"})
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != ErrorCodeMissingAPIKey {
		t.Fatalf("error = %v, coded = %+v", err, coded)
	}
}

func TestServiceFetchReader(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/http://example.com") {
			t.Fatalf("reader path = %q", r.URL.Path)
		}
		if got := r.Header.Get("X-Return-Format"); got != "markdown" {
			t.Errorf("X-Return-Format = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer jina-test" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Example\n\nBody\n"))
	}))
	t.Cleanup(server.Close)

	service := NewService(Config{
		AllowPrivateNetworks: true,
		ReaderEndpoint:       server.URL,
		JinaAPIKey:           "jina-test",
	})
	doc, err := service.Fetch(context.Background(), FetchRequest{URL: "http://example.com"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if doc.Source != "reader" || doc.Title != "Example" || doc.Content != "# Example\n\nBody\n" {
		t.Fatalf("document = %+v", doc)
	}
}

func TestServiceFetchReaderRejectsProviderErrorHeader(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		w.Header().Set("X-Respond-Error", "upstream reader failed")
		_, _ = w.Write([]byte("# This must not be emitted as a document\n"))
	}))
	t.Cleanup(server.Close)

	service := NewService(Config{AllowPrivateNetworks: true, ReaderEndpoint: server.URL})
	_, err := service.Fetch(context.Background(), FetchRequest{URL: "http://example.com"})
	if err == nil {
		t.Fatal("Fetch succeeded for provider error header")
	}
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != ErrorCodeProviderResponse || coded.Suggestion == "" {
		t.Fatalf("Fetch error = %v, coded = %+v", err, coded)
	}
}

func TestServiceFetchReaderRejectsKnownErrorPrefixes(t *testing.T) {
	t.Parallel()
	for _, prefix := range jinaErrorPrefixes {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/markdown")
				_, _ = w.Write([]byte(prefix + " provider failure"))
			}))
			t.Cleanup(server.Close)

			service := NewService(Config{AllowPrivateNetworks: true, ReaderEndpoint: server.URL})
			_, err := service.Fetch(context.Background(), FetchRequest{URL: "http://example.com"})
			var coded *CodedError
			if !errors.As(err, &coded) || coded.Code != ErrorCodeProviderResponse {
				t.Fatalf("Fetch error = %v, coded = %+v", err, coded)
			}
		})
	}
}

func TestServiceFetchReaderAllowsLateErrorTextAndShortContent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{name: "late error text", body: strings.Repeat("x", 501) + "\nError: this is article content"},
		{name: "short content", body: "OK"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/markdown")
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			service := NewService(Config{AllowPrivateNetworks: true, ReaderEndpoint: server.URL})
			doc, err := service.Fetch(context.Background(), FetchRequest{URL: "http://example.com"})
			if err != nil || doc.Content != tt.body {
				t.Fatalf("Fetch = document=%+v error=%v", doc, err)
			}
		})
	}
}

func TestCheckJinaSourceURL(t *testing.T) {
	t.Parallel()
	target, err := url.Parse("http://origin.example/page")
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	if err := checkJinaSourceURL(target, "Title: T\nURL Source: http://origin.example/page\nMarkdown Content:\nbody"); err != nil {
		t.Fatalf("same-host source: %v", err)
	}
	assertCodedError(t, checkJinaSourceURL(target, "Title: T\nURL Source: http://other.example/page\nMarkdown Content:\nbody"), ErrorCodeCrossHostRedirect)
	assertCodedError(t, checkJinaSourceURL(target, "Title: T\nURL Source: http://api.origin.example/page\nMarkdown Content:\nbody"), ErrorCodeCrossHostRedirect)
	if err := checkJinaSourceURL(target, "# Heading\n\nbody without metadata"); err != nil {
		t.Fatalf("missing metadata: %v", err)
	}
	if err := checkJinaSourceURL(target, "URL Source: ://malformed\nbody"); err != nil {
		t.Fatalf("malformed metadata: %v", err)
	}
}

func TestServiceFetchReaderProbesCrossHostRedirect(t *testing.T) {
	t.Parallel()
	var readerRequests atomic.Int32
	readerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		readerRequests.Add(1)
		_, _ = w.Write([]byte("# Hosted\n\nshould not be returned\n"))
	}))
	t.Cleanup(readerServer.Close)

	var port string
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "http://other.example:"+port+"/final", http.StatusFound)
		case "/final":
			_, _ = w.Write([]byte("cross-host content"))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	t.Cleanup(targetServer.Close)
	_, port, _ = net.SplitHostPort(targetServer.Listener.Addr().String())

	service := NewService(Config{AllowPrivateNetworks: true, ReaderEndpoint: readerServer.URL})
	_, err := service.Fetch(context.Background(), FetchRequest{URL: targetServer.URL + "/start", Reader: ReaderModeJina})
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != ErrorCodeCrossHostRedirect {
		t.Fatalf("Fetch error = %v, coded = %#v", err, coded)
	}
	if got := readerRequests.Load(); got != 0 {
		t.Fatalf("hosted reader requests = %d, want 0 (cross-host redirect must stop before Jina)", got)
	}
}

func TestMarkdownTitlePrefersJinaMetadata(t *testing.T) {
	t.Parallel()
	if got := markdownTitle("Title: Provider title\n\n# Markdown title\n"); got != "Provider title" {
		t.Fatalf("markdownTitle metadata = %q", got)
	}
	if got := markdownTitle("Title:   \n\n# Markdown title\n"); got != "Markdown title" {
		t.Fatalf("markdownTitle H1 fallback = %q", got)
	}
}

func TestServiceSearch(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "site:go.dev go testing" {
			t.Errorf("query = %q", got)
		}
		if got := r.URL.Query().Get("count"); got != "2" {
			t.Errorf("count = %q", got)
		}
		if got := r.Header.Get("X-Subscription-Token"); got != "brave-test" {
			t.Errorf("token = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"web": map[string]any{"results": []map[string]string{
				{"title": "Go Testing", "url": "https://go.dev", "description": "Testing in Go."},
			}},
		})
	}))
	t.Cleanup(server.Close)

	service := NewService(Config{
		AllowPrivateNetworks: true,
		SearchEndpoint:       server.URL,
		BraveAPIKey:          "brave-test",
	})
	result, err := service.Search(context.Background(), SearchRequest{Query: "go testing", Limit: 2, IncludeDomains: []string{"go.dev"}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Results) != 1 || result.Results[0].Title != "Go Testing" {
		t.Fatalf("result = %+v", result)
	}
}

func TestServiceSearchRequiresAPIKey(t *testing.T) {
	t.Parallel()
	service := NewService(Config{AllowPrivateNetworks: true})
	if _, err := service.Search(context.Background(), SearchRequest{Query: "go"}); err == nil || !strings.Contains(err.Error(), "BRAVE_API_KEY") {
		t.Fatalf("Search error = %v", err)
	}
}
