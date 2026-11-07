package webfetch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

const (
	maxAttempts     = 3
	maxRedirectHops = 10
	maxErrorBody    = 512
	baseRetryWait   = 150 * time.Millisecond
	maxRetryWait    = 2 * time.Second
)

var (
	errBodyTooLarge  = ErrTooLarge
	errRedirectLimit = errors.New("redirect limit exceeded")
)

type hostResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type contextDialer func(context.Context, string, string) (net.Conn, error)

// Client performs bounded HTTP requests with redirect and network safety checks.
type Client struct {
	httpClient           *http.Client
	maxBodyBytes         int64
	allowPrivateNetworks bool
	userAgent            string
	resolver             hostResolver
	dialer               contextDialer
}

// NewClient constructs a bounded HTTP client from explicit configuration.

func truncate(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	return value[:maxBytes] + "..."
}
