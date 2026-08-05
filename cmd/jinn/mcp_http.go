package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/voocel/mcp-sdk-go/server"
	"github.com/voocel/mcp-sdk-go/transport/streamhttp"
)

const (
	mcpHTTPDefaultAddr = "127.0.0.1:8788"
	mcpHTTPPath        = "/mcp"
	mcpHTTPMaxBody     = 8 << 20
	mcpHTTPTokenEnv    = "JINN_MCP_HTTP_TOKEN"
	mcpHTTPOriginsEnv  = "JINN_MCP_HTTP_ORIGINS"
)

type mcpHTTPConfig struct {
	addr    string
	token   string
	origins []string
}

func loadMCPHTTPConfig(addr string) (mcpHTTPConfig, error) {
	if strings.TrimSpace(addr) == "" {
		addr = mcpHTTPDefaultAddr
	}
	origins, err := parseMCPHTTPOrigins(os.Getenv(mcpHTTPOriginsEnv))
	if err != nil {
		return mcpHTTPConfig{}, err
	}
	token := strings.TrimSpace(os.Getenv(mcpHTTPTokenEnv))
	if err := validateMCPHTTPExposure(addr, token, origins); err != nil {
		return mcpHTTPConfig{}, err
	}
	return mcpHTTPConfig{addr: addr, token: token, origins: origins}, nil
}

func parseMCPHTTPOrigins(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	origins := make([]string, 0)
	seen := make(map[string]struct{})
	for _, raw := range strings.Split(value, ",") {
		origin := strings.TrimSpace(raw)
		if origin == "" {
			return nil, fmt.Errorf("%s contains an empty origin", mcpHTTPOriginsEnv)
		}
		if _, exists := seen[origin]; exists {
			return nil, fmt.Errorf("%s contains duplicate origin %q", mcpHTTPOriginsEnv, origin)
		}
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
			return nil, fmt.Errorf("%s origin %q must be an exact http or https origin", mcpHTTPOriginsEnv, origin)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("%s origin %q must use http or https", mcpHTTPOriginsEnv, origin)
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	return origins, nil
}

func validateMCPHTTPExposure(addr, token string, origins []string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid MCP HTTP address %q: expected host:port", addr)
	}
	host = strings.Trim(host, "[]")
	loopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if ip := net.ParseIP(host); ip != nil {
		loopback = ip.IsLoopback()
	}
	if loopback {
		return nil
	}
	if token == "" {
		return fmt.Errorf("non-loopback MCP HTTP requires %s", mcpHTTPTokenEnv)
	}
	if len(origins) == 0 {
		return fmt.Errorf("non-loopback MCP HTTP requires %s", mcpHTTPOriginsEnv)
	}
	return nil
}

func serveMCPHTTP(ctx context.Context, addr, ldVersion string, mode jinn.ShellMode, profile mcpProfile) error {
	config, err := loadMCPHTTPConfig(addr)
	if err != nil {
		return fail(jinn.Response{
			Error:      err.Error(),
			Suggestion: fmt.Sprintf("use loopback or set %s and %s for non-loopback MCP HTTP", mcpHTTPTokenEnv, mcpHTTPOriginsEnv),
			ErrorCode:  jinn.ErrCodeInvalidArgs,
		})
	}

	var engine *jinn.Engine
	if profile == mcpProfileReadOnly {
		workDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd for MCP HTTP read-only profile: %w", err)
		}
		engine, err = jinn.NewWithConfig(workDir, jinn.EngineConfig{
			Version:   ldVersion,
			ShellMode: jinn.ShellModeDisabled,
		})
		if err != nil {
			return fmt.Errorf("open MCP HTTP read-only workspace: %w", err)
		}
		defer func() { _ = engine.Close() }()
	}

	handler := newMCPHTTPHandler(newMCPServerWithProfile(ldVersion, mode, profile, engine), config)
	httpServer := &http.Server{
		Addr:              config.addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	listener, err := net.Listen("tcp", config.addr)
	if err != nil {
		return fmt.Errorf("listen for MCP HTTP on %s: %w", config.addr, err)
	}
	defer func() { _ = listener.Close() }()

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-sigCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	fmt.Fprintf(os.Stderr, "jinn MCP HTTP listening on http://%s%s\n", config.addr, mcpHTTPPath)
	err = httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func newMCPHTTPHandler(srv *server.Server, config mcpHTTPConfig) http.Handler {
	handler := streamhttp.NewHandler(srv, &streamhttp.Options{
		AllowedOrigins: config.origins,
		MaxBodyBytes:   mcpHTTPMaxBody,
	})
	protected := mcpHTTPAuth(config.token, handler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != mcpHTTPPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		protected.ServeHTTP(w, r)
	})
}

func mcpHTTPAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		authorization := r.Header.Get("Authorization")
		candidate := ""
		if strings.HasPrefix(authorization, prefix) {
			candidate = strings.TrimPrefix(authorization, prefix)
		}
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
