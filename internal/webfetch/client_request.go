package webfetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// NewClient constructs a bounded HTTP client from explicit configuration.
func NewClient(cfg ClientConfig) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxBodyBytes := cfg.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxBodyBytes
	}
	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = DefaultUserAgent
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 100
	transport.IdleConnTimeout = 90 * time.Second

	client := &Client{
		maxBodyBytes:         maxBodyBytes,
		allowPrivateNetworks: cfg.AllowPrivateNetworks,
		userAgent:            userAgent,
		resolver:             net.DefaultResolver,
		dialer:               (&net.Dialer{}).DialContext,
	}
	transport.DialContext = client.dialContext
	client.httpClient = &http.Client{
		Timeout:       timeout,
		Transport:     transport,
		CheckRedirect: client.checkRedirect,
	}
	return client
}

// Get performs a safe, retryable GET request.
//
//nolint:revive // httpResponse is deliberately internal because only package-owned providers consume raw responses.
func (c *Client) Get(ctx context.Context, rawURL string, headers http.Header) (httpResponse, error) {
	return c.Do(ctx, http.MethodGet, rawURL, headers, nil)
}

// GetWithMaxBodyBytes performs a GET with a per-request body limit. It exists
// for bounded media payloads whose limit differs from the general body limit.
//
//nolint:revive // httpResponse is deliberately internal because only package-owned providers consume raw responses.
func (c *Client) GetWithMaxBodyBytes(ctx context.Context, rawURL string, headers http.Header, maxBytes int64) (httpResponse, error) {
	return c.do(ctx, http.MethodGet, rawURL, headers, nil, maxBytes)
}

// Do performs one bounded HTTP operation and retries only safe methods.
//
//nolint:revive // httpResponse is deliberately internal because only package-owned providers consume raw responses.
func (c *Client) Do(ctx context.Context, method, rawURL string, headers http.Header, body []byte) (httpResponse, error) {
	return c.do(ctx, method, rawURL, headers, body, c.maxBodyBytes)
}

//nolint:funlen,gocognit,gocyclo,nestif,revive // Raw responses remain package-internal; the retry loop is one audit surface.
func (c *Client) do(ctx context.Context, method, rawURL string, headers http.Header, body []byte, maxBodyBytes int64) (httpResponse, error) {
	target, err := validateTarget(rawURL, c.allowPrivateNetworks)
	if err != nil {
		return httpResponse{}, err
	}

	attempts := requestAttempts(method)
	for attempt := 1; attempt <= attempts; attempt++ {
		var requestBody io.Reader
		if body != nil {
			requestBody = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, target.String(), requestBody)
		if err != nil {
			return httpResponse{}, fmt.Errorf("create request: %w", newCodedError(err, ErrorCodeInvalidURL, "provide a valid http or https URL"))
		}
		if headers == nil {
			req.Header = make(http.Header)
		} else {
			req.Header = headers.Clone()
		}
		req.Header.Set("User-Agent", c.userAgent)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			if ctx.Err() != nil {
				return httpResponse{}, codeContextError(ctx.Err())
			}
			if errors.Is(err, errRedirectLimit) {
				return httpResponse{}, newCodedError(
					fmt.Errorf("%s %s: %w", method, target, err),
					ErrorCodeUpstreamHTTP,
					"check the redirect target or use a URL with fewer redirects",
				)
			}
			var coded *CodedError
			if errors.As(err, &coded) {
				return httpResponse{}, err
			}
			if attempt == attempts {
				return httpResponse{}, fmt.Errorf("%s %s: %w", method, target, newCodedError(err, ErrorCodeUpstreamHTTP, "check the URL and network connectivity, then retry"))
			}
			if err := waitForRetry(ctx, retryDelay(attempt, nil)); err != nil {
				return httpResponse{}, codeContextError(err)
			}
			continue
		}

		retrying := shouldRetry(resp.StatusCode) && attempt < attempts
		var responseBody []byte
		var readErr error
		if retrying {
			readErr = drainBounded(resp.Body, maxBodyBytes)
		} else {
			responseBody, readErr = readBounded(resp.Body, maxBodyBytes)
		}
		closeErr := resp.Body.Close()
		if readErr != nil {
			if errors.Is(readErr, ErrTooLarge) {
				readErr = newCodedError(readErr, ErrorCodeResponseTooLarge, "reduce the response or configure a larger response limit")
			}
			return httpResponse{}, fmt.Errorf("read %s response: %w", target, readErr)
		}
		if closeErr != nil {
			return httpResponse{}, fmt.Errorf("close %s response: %w", target, closeErr)
		}

		if retrying {
			if err := waitForRetry(ctx, retryDelay(attempt, resp)); err != nil {
				return httpResponse{}, codeContextError(err)
			}
			continue
		}

		result := httpResponse{
			URL:        target.String(),
			StatusCode: resp.StatusCode,
			Header:     resp.Header.Clone(),
			Body:       responseBody,
		}
		if resp.Request != nil && resp.Request.URL != nil {
			result.URL = resp.Request.URL.String()
		}
		if err := responseStatusError(result.URL, resp.StatusCode, resp.Status, string(responseBody)); err != nil {
			return httpResponse{}, err
		}
		return result, nil
	}

	return httpResponse{}, newCodedError(fmt.Errorf("%s %s: retry attempts exhausted", method, target), ErrorCodeUpstreamHTTP, "retry the request later")
}
