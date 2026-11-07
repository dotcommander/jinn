package webfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveSearchProviderEnvPrecedence(t *testing.T) {
	t.Parallel()
	cfg := Config{BraveAPIKey: "config-key", SearchEndpoint: "https://config.example/search"}
	client := NewClient(ClientConfig{})
	provider, configured, err := resolveSearchProvider(providerBrave, client, cfg)
	if err != nil || !configured || provider == nil {
		t.Fatalf("resolve: provider=%v configured=%v err=%v", provider != nil, configured, err)
	}
	brave, ok := provider.(braveProvider)
	if !ok {
		t.Fatalf("provider type = %T, want braveProvider", provider)
	}
	if brave.apiKey != "config-key" {
		t.Fatalf("apiKey = %q, want config-key", brave.apiKey)
	}
	if brave.endpoint != "https://config.example/search" {
		t.Fatalf("endpoint = %q, want config endpoint", brave.endpoint)
	}
}

func TestResolveSearchProviderUnconfigured(t *testing.T) {
	t.Parallel()
	client := NewClient(ClientConfig{})
	_, configured, err := resolveSearchProvider(providerBrave, client, Config{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if configured {
		t.Fatal("configured = true, want false with no key anywhere")
	}
	if _, _, err := resolveSearchProvider("nosuch", client, Config{}); err == nil {
		t.Fatal("unknown provider resolved without error")
	}
}

func TestFetchGitHubRepoInterceptor(t *testing.T) {
	t.Parallel()
	var sawAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tree":[{"path":"README.md","type":"blob","size":42},{"path":"logo.png","type":"blob","size":99}],"truncated":false}`))
	}))
	t.Cleanup(server.Close)

	service := NewService(Config{
		AllowPrivateNetworks: true,
		GitHubAPIEndpoint:    server.URL,
		GitHubToken:          "test-token",
	})
	doc, err := service.Fetch(context.Background(), FetchRequest{URL: "https://github.com/owner/repo"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if doc.Source != "github" {
		t.Fatalf("source = %q, want github", doc.Source)
	}
	if sawAuth != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want Bearer test-token", sawAuth)
	}
	if !strings.Contains(doc.Content, "README.md") || !strings.Contains(doc.Content, "logo.png") {
		t.Fatalf("content = %q", doc.Content)
	}
}
