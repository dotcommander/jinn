package webfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuditC5NativeChromeLocalSafetyAndStatus(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("native browser integration")
	}
	chromePath := strings.TrimSpace(os.Getenv("JINN_WEB_TEST_CHROME_PATH"))
	if chromePath == "" {
		t.Skip("JINN_WEB_TEST_CHROME_PATH is not set")
	}
	var privateContacts atomic.Int32
	var privateURL string
	body := `<html><body><article><h1>Controlled article</h1><p>` + strings.Repeat("Local browser acceptance content. ", 30) + `</p></article><img src="PRIVATE/private"></body></html>`
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch req.URL.Path {
		case "/private":
			privateContacts.Add(1)
		case "/redirect":
			http.Redirect(w, req, privateURL+"/private", http.StatusFound)
			return
		case "/404":
			w.WriteHeader(http.StatusNotFound)
		case "/500":
			w.WriteHeader(http.StatusInternalServerError)
		}
		_, _ = io.WriteString(w, strings.ReplaceAll(body, "PRIVATE", privateURL))
	}))
	t.Cleanup(origin.Close)
	privateURL = origin.URL
	local, _ := url.Parse(origin.URL)
	publicURL := "http://public.example:" + local.Port()
	renderer, err := newChromedpRenderer(rendererConfig{Timeout: 15 * time.Second, MaxConcurrency: 1, MaxRequests: 50,
		MaxNetworkBytes: 1 << 20, MaxHTMLBytes: 1 << 20, ChromePath: chromePath})
	if err != nil {
		t.Fatal(err)
	}
	r := renderer.(*chromedpRenderer)
	r.client.resolver = resolverFunc(func(_ context.Context, host string) ([]net.IPAddr, error) {
		if host != "public.example" {
			return nil, fmt.Errorf("fixture rejects host %s", host)
		}
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	})
	r.client.dialer = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != net.JoinHostPort("93.184.216.34", local.Port()) {
			return nil, fmt.Errorf("fixture rejects dial %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, local.Host)
	}
	service := NewService(Config{})
	service.renderer = renderer
	t.Cleanup(func() { _ = service.Close(t.Context()) })
	doc, err := service.Fetch(t.Context(), FetchRequest{URL: publicURL + "/page", Reader: ReaderModeDefuddle, Render: RenderModeAlways})
	if err != nil || !strings.Contains(doc.Content, "Controlled article") {
		t.Fatalf("valid document with blocked subresource: %+v, %v", doc, err)
	}
	for _, target := range []string{privateURL + "/private", publicURL + "/redirect"} {
		if _, err := service.Fetch(t.Context(), FetchRequest{URL: target, Reader: ReaderModeDefuddle, Render: RenderModeAlways}); err == nil {
			t.Fatalf("unsafe main navigation accepted: %s", target)
		}
	}
	for _, status := range []int{404, 500} {
		_, err := service.Fetch(t.Context(), FetchRequest{URL: fmt.Sprintf("%s/%d", publicURL, status), Reader: ReaderModeDefuddle, Render: RenderModeAlways})
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != status || httpErr.Body == "" {
			t.Fatalf("rendered %d: %v", status, err)
		}
	}
	if got := privateContacts.Load(); got != 0 {
		t.Fatalf("prohibited private contacts = %d", got)
	}
}
