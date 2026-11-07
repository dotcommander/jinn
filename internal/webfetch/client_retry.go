package webfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func requestAttempts(method string) int {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return maxAttempts
	default:
		return 1
	}
}

func httpErrorCode(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrorCodeAuthentication
	case http.StatusTooManyRequests:
		return ErrorCodeRateLimited
	default:
		return ErrorCodeUpstreamHTTP
	}
}

func validateTarget(rawURL string, allowPrivateNetworks bool) (*url.URL, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil, newCodedError(errors.New("URL is required"), ErrorCodeInvalidURL, "provide an absolute http or https URL")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, newCodedError(fmt.Errorf("parse URL: %w", err), ErrorCodeInvalidURL, "provide a valid absolute http or https URL")
	}
	if err := validateURL(u, allowPrivateNetworks); err != nil {
		return nil, err
	}
	return rewriteGitHubBlobURL(u), nil
}

func validateURL(u *url.URL, allowPrivateNetworks bool) error {
	if u == nil {
		return newCodedError(errors.New("URL is required"), ErrorCodeInvalidURL, "provide an absolute http or https URL")
	}
	if u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS {
		return newCodedError(fmt.Errorf("unsupported URL scheme %q: only http and https are allowed", u.Scheme), ErrorCodeInvalidURL, "use an http or https URL")
	}
	if u.Hostname() == "" {
		return newCodedError(errors.New("URL host is required"), ErrorCodeInvalidURL, "include a hostname in the URL")
	}
	if u.User != nil {
		return newCodedError(errors.New("URLs with embedded credentials are not allowed"), ErrorCodeInvalidURL, "remove embedded username and password credentials from the URL")
	}
	if allowPrivateNetworks {
		return nil
	}
	return validateHost(u.Hostname())
}

// rewriteGitHubBlobURL maps github.com blob/raw URLs to the same file on
// raw.githubusercontent.com so fetches return file content instead of page
// chrome. Non-GitHub and non-blob/raw shapes are returned unchanged.
func rewriteGitHubBlobURL(u *url.URL) *url.URL {
	host := strings.ToLower(u.Hostname())
	if host != hostGitHubCom && host != "www.github.com" {
		return u
	}
	var segments []string
	for _, raw := range strings.Split(u.EscapedPath(), "/") {
		segment := strings.TrimSpace(raw)
		if segment == "" {
			continue
		}
		decoded, err := url.PathUnescape(segment)
		if err != nil || decoded == "" || strings.Contains(decoded, "/") {
			return u
		}
		segments = append(segments, decoded)
	}
	if len(segments) < 5 {
		return u
	}
	if segments[2] != string(githubURLBlob) && segments[2] != "raw" {
		return u
	}
	rewritten := *u
	rewritten.Scheme = schemeHTTPS
	rewritten.Host = "raw.githubusercontent.com"
	rewritten.Path = "/" + strings.Join(segments[:2], "/") + "/" + strings.Join(segments[3:], "/")
	rewritten.RawQuery = ""
	rewritten.Fragment = ""
	return &rewritten
}

func readBounded(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBodyBytes
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errBodyTooLarge
	}
	return data, nil
}

func drainBounded(r io.Reader, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBodyBytes
	}
	n, err := io.Copy(io.Discard, io.LimitReader(r, maxBytes+1))
	if err != nil {
		return err
	}
	if n > maxBytes {
		return errBodyTooLarge
	}
	return nil
}

func shouldRetry(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func httpErrorSuggestion(statusCode int) string {
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return "check the target URL and provider credentials"
	}
	if statusCode == http.StatusTooManyRequests {
		return "wait and retry with fewer requests"
	}
	if shouldRetry(statusCode) {
		return "retry the request later or check the upstream service status"
	}
	return "check the target URL and upstream response"
}

//nolint:nestif // Retry-After supports two wire formats before falling back to exponential delay.
func retryDelay(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if value := strings.TrimSpace(resp.Header.Get("Retry-After")); value != "" {
			if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
				return min(time.Duration(seconds)*time.Second, maxRetryWait)
			}
			if when, err := http.ParseTime(value); err == nil {
				return min(max(time.Until(when), 0), maxRetryWait)
			}
		}
	}
	wait := baseRetryWait * time.Duration(1<<(attempt-1))
	return min(wait, maxRetryWait)
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
