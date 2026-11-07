package webfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

type trackedResponseBody struct {
	reader   io.Reader
	read     int
	closed   bool
	closeErr error
}

func (b *trackedResponseBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	b.read += n
	return n, err
}

func (b *trackedResponseBody) Close() error {
	b.closed = true
	return b.closeErr
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestValidateURLRejectsUnsafeTargets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: "", want: "URL is required"},
		{name: "scheme", raw: "file:///tmp/example", want: "unsupported URL scheme"},
		{name: "credentials", raw: "https://user:pass@example.com", want: "embedded credentials"},
		{name: "loopback", raw: "http://127.0.0.1:8080", want: "private address"},
		{name: "carrier-grade NAT", raw: "http://100.64.0.1", want: "private address"},
		{name: "localhost", raw: "http://localhost:8080", want: "private host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := validateTarget(tt.raw, false)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateTarget(%q) error = %v, want substring %q", tt.raw, err, tt.want)
			}
		})
	}
}

func TestValidateURLAllowsPrivateTargetsWhenConfigured(t *testing.T) {
	t.Parallel()
	if _, err := validateTarget("http://127.0.0.1:8080/path", true); err != nil {
		t.Fatalf("validateTarget with private networks enabled: %v", err)
	}
}

func TestReadBoundedRejectsOversizedBody(t *testing.T) {
	t.Parallel()
	_, err := readBounded(strings.NewReader("12345"), 4)
	if !errors.Is(err, errBodyTooLarge) {
		t.Fatalf("readBounded error = %v, want %v", err, errBodyTooLarge)
	}
}

func TestClientRejectsPrivateRedirect(t *testing.T) {
	t.Parallel()
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("private"))
	}))
	private.Close()

	client := NewClient(ClientConfig{})
	if _, err := client.Get(context.Background(), private.URL, nil); err == nil {
		t.Fatal("expected private target to be rejected")
	}
}

func TestClientClassifiesHTTPStatuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		code int
		want string
	}{
		{name: "unauthorized", code: http.StatusUnauthorized, want: ErrorCodeAuthentication},
		{name: "forbidden", code: http.StatusForbidden, want: ErrorCodeAuthentication},
		{name: "rate limited", code: http.StatusTooManyRequests, want: ErrorCodeRateLimited},
		{name: "generic upstream", code: http.StatusInternalServerError, want: ErrorCodeUpstreamHTTP},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(tt.code)
				_, _ = w.Write([]byte("bounded error"))
			}))
			t.Cleanup(server.Close)

			client := NewClient(ClientConfig{AllowPrivateNetworks: true})
			_, err := client.Get(context.Background(), server.URL, nil)
			var coded *CodedError
			if !errors.As(err, &coded) || coded.Code != tt.want {
				t.Fatalf("error = %v, coded = %+v, want %q", err, coded, tt.want)
			}
		})
	}
}

func TestClientTransportErrorReportsActualMethod(t *testing.T) {
	t.Parallel()
	client := NewClient(ClientConfig{AllowPrivateNetworks: true})
	calls := 0
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("injected %s failure", req.Method)
	})
	_, err := client.Do(context.Background(), http.MethodPost, "https://example.com/search", nil, []byte(`{}`))
	var coded *CodedError
	if !errors.As(err, &coded) || coded.Code != ErrorCodeUpstreamHTTP {
		t.Fatalf("error = %#v", err)
	}
	if !strings.Contains(err.Error(), "POST https://example.com/search") || strings.Contains(err.Error(), "GET https://example.com/search") {
		t.Fatalf("error = %q", err)
	}
	if calls != 1 {
		t.Fatalf("POST attempts = %d, want 1", calls)
	}
}

func TestGETRetryKeepsOriginalEmptyBody(t *testing.T) {
	t.Parallel()
	client := NewClient(ClientConfig{})
	calls := 0
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		var data []byte
		var err error
		if req.Body != nil {
			data, err = io.ReadAll(req.Body)
		}
		if err != nil || len(data) != 0 || req.ContentLength != 0 {
			t.Errorf("GET attempt %d: body = %q, length = %d, error = %v", calls, data, req.ContentLength, err)
		}
		status := http.StatusTooManyRequests
		content := "retry error body"
		if calls == 2 {
			status = http.StatusOK
			content = "success"
		}
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     http.Header{"Retry-After": []string{"0"}},
			Body:       io.NopCloser(strings.NewReader(content)),
			Request:    req,
		}, nil
	})
	resp, err := client.Get(context.Background(), "https://example.com/article", nil)
	if err != nil || calls != 2 || string(resp.Body) != "success" {
		t.Fatalf("GET = (%+v, %v), calls = %d", resp, err, calls)
	}
}

func TestGETRetryDrainsIntermediateBody(t *testing.T) {
	t.Parallel()
	client := NewClient(ClientConfig{MaxBodyBytes: 4})
	first := &trackedResponseBody{reader: strings.NewReader("fail")}
	calls := 0
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 2 && !first.closed {
			t.Error("retry started before first response body was closed")
		}
		status, body := http.StatusServiceUnavailable, io.ReadCloser(first)
		if calls == 2 {
			status = http.StatusOK
			body = io.NopCloser(strings.NewReader("done"))
		}
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     http.Header{"Retry-After": []string{"0"}},
			Body:       body,
			Request:    req,
		}, nil
	})
	resp, err := client.Get(context.Background(), "https://example.com/article", nil)
	if err != nil || calls != 2 || resp.StatusCode != http.StatusOK || string(resp.Body) != "done" || resp.URL != "https://example.com/article" {
		t.Fatalf("GET = (%+v, %v), calls = %d", resp, err, calls)
	}
	if !first.closed || first.read != 4 {
		t.Fatalf("intermediate body: closed = %v, bytes read = %d", first.closed, first.read)
	}
}

func TestGETRetryDrainBoundariesAndErrors(t *testing.T) {
	t.Parallel()
	readFailure := errors.New("injected read failure")
	closeFailure := errors.New("injected close failure")
	tests := []struct {
		name     string
		reader   io.Reader
		closeErr error
		wantRead int
		wantErr  error
		wantCode string
		attempts int
	}{
		{name: "exact limit", reader: strings.NewReader("1234"), wantRead: 4, attempts: 2},
		{name: "limit plus one", reader: strings.NewReader("123456"), wantRead: 5, wantErr: ErrTooLarge, wantCode: ErrorCodeResponseTooLarge, attempts: 1},
		{name: "read failure", reader: io.MultiReader(strings.NewReader("12"), failingReader{readFailure}), wantRead: 2, wantErr: readFailure, attempts: 1},
		{name: "close failure", reader: strings.NewReader("12"), closeErr: closeFailure, wantRead: 2, wantErr: closeFailure, attempts: 1},
		{name: "read before close failure", reader: failingReader{readFailure}, closeErr: closeFailure, wantErr: readFailure, attempts: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := NewClient(ClientConfig{MaxBodyBytes: 4})
			first := &trackedResponseBody{reader: tt.reader, closeErr: tt.closeErr}
			calls := 0
			client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if calls == 2 && !first.closed {
					t.Error("retry started before first body was closed")
				}
				status, body := http.StatusServiceUnavailable, io.ReadCloser(first)
				if calls == 2 {
					status = http.StatusOK
					body = io.NopCloser(strings.NewReader("done"))
				}
				return &http.Response{
					StatusCode: status,
					Status:     http.StatusText(status),
					Header:     http.Header{"Retry-After": []string{"0"}},
					Body:       body,
					Request:    req,
				}, nil
			})
			resp, err := client.Get(context.Background(), "https://example.com/article", nil)
			if !errors.Is(err, tt.wantErr) || calls != tt.attempts {
				t.Fatalf("GET = (%+v, %v), calls = %d; want error %v, calls %d", resp, err, calls, tt.wantErr, tt.attempts)
			}
			if tt.wantErr == nil && string(resp.Body) != "done" {
				t.Fatalf("final body = %q, want done", resp.Body)
			}
			if !first.closed || first.read != tt.wantRead {
				t.Fatalf("intermediate body: closed = %v, bytes read = %d; want %d", first.closed, first.read, tt.wantRead)
			}
			if tt.wantCode != "" {
				var coded *CodedError
				if !errors.As(err, &coded) || coded.Code != tt.wantCode {
					t.Fatalf("error = %v, want code %q", err, tt.wantCode)
				}
			}
		})
	}
}

func TestGETFinalRetryAttemptRetainsBody(t *testing.T) {
	t.Parallel()
	client := NewClient(ClientConfig{MaxBodyBytes: 16})
	calls := 0
	client.httpClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Status:     http.StatusText(http.StatusServiceUnavailable),
			Header:     http.Header{"Retry-After": []string{"0"}},
			Body:       io.NopCloser(strings.NewReader(fmt.Sprintf("attempt %d", calls))),
			Request:    req,
		}, nil
	})
	_, err := client.Get(context.Background(), "https://example.com/article", nil)
	var httpErr *HTTPError
	if calls != maxAttempts || !errors.As(err, &httpErr) || httpErr.Body != "attempt 3" {
		t.Fatalf("GET error = %v, attempts = %d; want final attempt body", err, calls)
	}
}

func TestRequestAttemptsRetriesOnlySafeMethods(t *testing.T) {
	t.Parallel()
	for method, want := range map[string]int{
		http.MethodGet: maxAttempts, http.MethodHead: maxAttempts, http.MethodOptions: maxAttempts,
		http.MethodPost: 1, http.MethodPut: 1, http.MethodDelete: 1,
	} {
		if got := requestAttempts(method); got != want {
			t.Errorf("requestAttempts(%q) = %d, want %d", method, got, want)
		}
	}
}
