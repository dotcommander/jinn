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

func mustParseGitHubURL(t *testing.T, raw string) githubURLInfo {
	t.Helper()
	target, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse URL %q: %v", raw, err)
	}
	info, ok := parseGitHubURL(target)
	if !ok {
		t.Fatalf("parseGitHubURL(%q) = not claimed", raw)
	}
	return info
}

func TestParseGitHubURL(t *testing.T) {
	t.Parallel()

	info := mustParseGitHubURL(t, "https://github.com/dotcommander/jinn")
	if info.owner != "dotcommander" || info.repo != "jinn" || info.kind != githubURLRoot || info.ref != "" {
		t.Fatalf("root parse = %+v", info)
	}

	info = mustParseGitHubURL(t, "https://github.com/dotcommander/jinn/blob/main/go.mod")
	if info.kind != githubURLBlob || info.ref != "main" || info.path != "go.mod" || info.refIsFullSHA {
		t.Fatalf("blob parse = %+v", info)
	}

	info = mustParseGitHubURL(t, "https://github.com/dotcommander/jinn/tree/v1.2.3/internal/webfetch")
	if info.kind != githubURLTree || info.ref != "v1.2.3" || info.path != "internal/webfetch" {
		t.Fatalf("tree parse = %+v", info)
	}

	sha := strings.Repeat("a", 40)
	info = mustParseGitHubURL(t, "https://github.com/o/r/blob/"+sha+"/dir/f.go")
	if !info.refIsFullSHA || info.ref != sha {
		t.Fatalf("sha parse = %+v", info)
	}

	for _, raw := range []string{
		"https://github.com/o/r/issues/12",
		"https://github.com/o/r/pull/3",
		"https://github.com/o/r/wiki/Home",
		"https://github.com/o/r/releases/tag/v1",
		"https://github.com/o/r/commits/main",
		"https://github.com/o",             // missing repo
		"https://example.com/o/r",          // wrong host
		"https://github.com/o/r/unknown/x", // unknown action
	} {
		target, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse URL %q: %v", raw, err)
		}
		if _, ok := parseGitHubURL(target); ok {
			t.Fatalf("parseGitHubURL(%q) = claimed, want unclaimed", raw)
		}
	}
}

func TestIsFullGitSHA(t *testing.T) {
	t.Parallel()
	if !isFullGitSHA(strings.Repeat("9f", 20)) {
		t.Fatal("40 hex chars should be a full SHA")
	}
	for _, ref := range []string{"main", "v1.2.3", strings.Repeat("g", 40), strings.Repeat("A", 40), ""} {
		if isFullGitSHA(ref) {
			t.Fatalf("isFullGitSHA(%q) = true, want false", ref)
		}
	}
}

func githubTestService(t *testing.T, handler http.HandlerFunc) *Service {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewService(Config{
		AllowPrivateNetworks: true,
		GitHubAPIEndpoint:    server.URL,
	})
}

func TestInterceptBlobNotClaimed(t *testing.T) {
	t.Parallel()
	called := false
	service := githubTestService(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	})

	// Blob URLs are rewritten to raw.githubusercontent.com by validateTarget
	// before Fetch reaches interceptors; a direct intercept must decline
	// them so the generic fetch keeps owning raw file content.
	target, err := url.Parse("https://github.com/dotcommander/jinn/blob/main/go.mod")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, claimed, err := service.interceptors[0].intercept(context.Background(), target); err != nil || claimed {
		t.Fatalf("intercept = claimed %v, err %v; want unclaimed", claimed, err)
	}
	if called {
		t.Fatal("interceptor called the API for a blob URL")
	}
}

func TestInterceptTreeUsesRefAndPath(t *testing.T) {
	t.Parallel()
	var gotPath string
	service := githubTestService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if gotPath != "/repos/dotcommander/jinn/git/trees/v1.0.0" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tree": []map[string]any{
				{"path": "internal/webfetch/github.go", "type": "blob", "size": 100},
				{"path": "README.md", "type": "blob", "size": 50},
			},
		})
	})

	doc, err := service.Fetch(context.Background(), FetchRequest{URL: "https://github.com/dotcommander/jinn/tree/v1.0.0/internal/webfetch"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotPath != "/repos/dotcommander/jinn/git/trees/v1.0.0" {
		t.Fatalf("API path = %q", gotPath)
	}
	if !strings.Contains(doc.Content, "internal/webfetch/github.go") {
		t.Fatalf("content missing scoped entry: %q", doc.Content)
	}
	if strings.Contains(doc.Content, "README.md") {
		t.Fatalf("content includes out-of-scope entry: %q", doc.Content)
	}
	if doc.Title != "dotcommander/jinn - internal/webfetch" {
		t.Fatalf("title = %q", doc.Title)
	}
}

func TestInterceptMultiSegmentBranchRef(t *testing.T) {
	t.Parallel()
	var attempts []string
	service := githubTestService(t, func(w http.ResponseWriter, r *http.Request) {
		attempts = append(attempts, r.URL.Path)
		if !strings.HasSuffix(r.URL.Path, "/git/trees/feature/foo") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tree": []map[string]any{
				{"path": "internal/webfetch/github.go", "type": "blob", "size": 10},
			},
		})
	})

	doc, err := service.Fetch(context.Background(), FetchRequest{URL: "https://github.com/o/r/tree/feature/foo/internal/webfetch"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(attempts) != 3 {
		t.Fatalf("API attempts = %v, want longest ref first then shortened", attempts)
	}
	if !strings.HasSuffix(attempts[2], "/git/trees/feature/foo") {
		t.Fatalf("third attempt = %q, want ref feature/foo", attempts[2])
	}
	if doc.Title != "o/r - internal/webfetch" {
		t.Fatalf("title = %q", doc.Title)
	}
	if !strings.Contains(doc.Content, "internal/webfetch/github.go") {
		t.Fatalf("content = %q", doc.Content)
	}
}

func TestInterceptNonCodeURLFallsThrough(t *testing.T) {
	t.Parallel()
	called := false
	service := githubTestService(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	})

	target, err := url.Parse("https://github.com/o/r/issues/12")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	interceptor := service.interceptors[0]
	if _, claimed, err := interceptor.intercept(context.Background(), target); err != nil || claimed {
		t.Fatalf("intercept = claimed %v, err %v; want unclaimed", claimed, err)
	}
	if called {
		t.Fatal("interceptor called the API for a non-code URL")
	}
}
