// Package mcpexplore owns MCP explorer targets, transports, and client lifecycle.
package mcpexplore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/voocel/mcp-sdk-go/client"
	"github.com/voocel/mcp-sdk-go/protocol"
	"github.com/voocel/mcp-sdk-go/transport"
	"github.com/voocel/mcp-sdk-go/transport/stdio"
	"github.com/voocel/mcp-sdk-go/transport/streamhttp"
)

// HTTPTokenEnv is the only bearer-token source for HTTP explorer targets.
const HTTPTokenEnv = "JINN_MCP_HTTP_TOKEN" //nolint:gosec // Environment variable name, not a credential.

const (
	transportHTTP  = "http"
	transportStdio = "stdio"
)

// Target identifies either an HTTP MCP endpoint or an explicit-argv subprocess.
type Target struct {
	Endpoint            string
	Command             string
	CommandArgs         []string
	TokenEnv            string
	CommandEnv          []string
	DisableDefaultToken bool
}

// Validate checks the target without starting a transport.
func (t Target) Validate() error {
	if t.Command != "" {
		return nil
	}
	u, err := url.Parse(t.Endpoint)
	if err != nil {
		return errors.New("invalid MCP endpoint: use an http:// or https:// URL")
	}
	if u.User != nil {
		return errors.New("MCP endpoint must not include embedded credentials")
	}
	if u.Host == "" || (u.Scheme != transportHTTP && u.Scheme != "https") {
		return fmt.Errorf("invalid MCP endpoint %q: use an http:// or https:// URL", t.Endpoint)
	}
	return nil
}

// New opens a client for target. Close releases the underlying transport.
//
//nolint:nestif // HTTP and stdio setup intentionally share one lifecycle constructor.
func New(ctx context.Context, target Target) (*Client, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}
	var t transport.Transport
	if target.Command != "" {
		command := NewCommandContext(ctx, target.Command, target.CommandArgs...)
		if target.CommandEnv != nil {
			command.Env = target.CommandEnv
		}
		stdioTransport, err := stdio.NewCommand(command, &stdio.CommandOptions{Stderr: io.Discard, TerminateDuration: 5 * time.Second})
		if err != nil {
			return nil, err
		}
		t = stdioTransport
	} else {
		httpClient, err := httpClientForTarget(target)
		if err != nil {
			return nil, err
		}
		t = streamhttp.New(target.Endpoint, &streamhttp.TransportOptions{HTTPClient: httpClient, MaxRetries: -1})
	}
	return &Client{client: client.New(t, &client.Options{Info: &protocol.Implementation{Name: "jinn", Version: "dev"}, Logger: slog.New(slog.DiscardHandler)})}, nil
}

// Client exposes the discovery-only MCP explorer operations.
type Client struct{ client *client.Client }

// Close releases the selected MCP transport and subprocess, if any.
func (c *Client) Close() error { return c.client.Close() }

// Discover returns the server identity and capabilities.
func (c *Client) Discover(ctx context.Context) (*protocol.DiscoverResult, error) {
	return c.client.Discover(ctx)
}

// ListTools returns every tools/list page in server order.
func (c *Client) ListTools(ctx context.Context) ([]*protocol.Tool, error) {
	tools := make([]*protocol.Tool, 0)
	for tool, err := range c.client.Tools(ctx) {
		if err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}
	return tools, nil
}

// Inspect returns a listed tool by its exact MCP name.
func (c *Client) Inspect(ctx context.Context, name string) (*protocol.Tool, error) {
	tools, err := c.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	for _, tool := range tools {
		if tool.Name == name {
			return tool, nil
		}
	}
	return nil, fmt.Errorf("MCP tool %q not found", name)
}

// Call invokes one MCP tool. Tool-level isError results are returned normally.
func (c *Client) Call(ctx context.Context, name string, arguments map[string]any) (*protocol.CallToolResult, error) {
	return c.client.CallTool(ctx, &protocol.CallToolParams{Name: name, Arguments: arguments})
}

// NewCommand constructs a subprocess without a shell and strips the HTTP token.
func NewCommand(name string, args ...string) *exec.Cmd {
	return NewCommandContext(context.Background(), name, args...)
}

// NewCommandContext constructs an explicit-argv subprocess bound to ctx.
func NewCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	command := exec.CommandContext(ctx, name, args...) //nolint:gosec // Explicit argv is the required shell-free transport contract.
	command.Env = CommandEnvironment(os.Environ())
	return command
}

// CommandEnvironment removes the HTTP bearer token before launching subprocesses.
func CommandEnvironment(environ []string) []string {
	tokenPrefix := HTTPTokenEnv + "="
	filtered := make([]string, 0, len(environ))
	for _, entry := range environ {
		if !strings.HasPrefix(entry, tokenPrefix) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

type roundTripper struct {
	base    http.RoundTripper
	headers http.Header
	origin  string
}

func (t roundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if canonicalOrigin(request.URL) != t.origin {
		return nil, errors.New("MCP endpoint redirected to a different origin")
	}
	clone := request.Clone(request.Context())
	for key, values := range t.headers {
		clone.Header[key] = append([]string(nil), values...)
	}
	return t.base.RoundTrip(clone)
}

// NewHTTPClient authenticates same-origin requests and blocks cross-origin redirects.
func NewHTTPClient(endpoint string) (*http.Client, error) {
	return buildHTTPClient(endpoint, HTTPTokenEnv)
}

func httpClientForTarget(target Target) (*http.Client, error) {
	tokenEnv := target.TokenEnv
	if tokenEnv == "" && !target.DisableDefaultToken {
		tokenEnv = HTTPTokenEnv
	}
	return buildHTTPClient(target.Endpoint, tokenEnv)
}

func buildHTTPClient(endpoint, tokenEnv string) (*http.Client, error) {
	if err := (Target{Endpoint: endpoint}).Validate(); err != nil {
		return nil, err
	}
	u, _ := url.Parse(endpoint)
	origin := canonicalOrigin(u)
	headers := make(http.Header)
	if token := strings.TrimSpace(os.Getenv(tokenEnv)); token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	return &http.Client{
		Transport: roundTripper{base: http.DefaultTransport, headers: headers, origin: origin},
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if canonicalOrigin(request.URL) != origin {
				return errors.New("MCP endpoint redirected to a different origin")
			}
			return nil
		},
	}, nil
}

func canonicalOrigin(u *url.URL) string {
	if u == nil {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	return scheme + "://" + host
}
