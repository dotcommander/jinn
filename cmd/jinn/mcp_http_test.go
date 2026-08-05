package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/voocel/mcp-sdk-go/protocol"
)

const mcpHTTPTestMeta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`

func TestMCPHTTPCLIParsing(t *testing.T) {
	t.Parallel()
	mode, profile, positional, err := parseCLIArgs([]string{"--mcp-profile=read-only", "--mcp-http", "127.0.0.1:9900"})
	if err != nil {
		t.Fatalf("parseCLIArgs: %v", err)
	}
	if mode != jinn.ShellModeDisabled || profile != mcpProfileReadOnly {
		t.Fatalf("parsed mode/profile = %q/%q", mode, profile)
	}
	if len(positional) != 2 || positional[0] != "--mcp-http" || positional[1] != "127.0.0.1:9900" {
		t.Fatalf("parsed positional = %#v", positional)
	}
}

func TestMCPHTTPDefaultConfig(t *testing.T) {
	t.Setenv(mcpHTTPTokenEnv, "")
	t.Setenv(mcpHTTPOriginsEnv, "")
	config, err := loadMCPHTTPConfig("")
	if err != nil {
		t.Fatalf("load default HTTP config: %v", err)
	}
	if config.addr != mcpHTTPDefaultAddr {
		t.Fatalf("default HTTP address = %q, want %q", config.addr, mcpHTTPDefaultAddr)
	}
	if config.token != "" || len(config.origins) != 0 {
		t.Fatalf("default HTTP security config = %#v", config)
	}
}

func TestMCPHTTPOriginParsing(t *testing.T) {
	t.Parallel()
	origins, err := parseMCPHTTPOrigins("https://agent.example.com, http://localhost:3000")
	if err != nil {
		t.Fatalf("parse origins: %v", err)
	}
	if len(origins) != 2 || origins[0] != "https://agent.example.com" || origins[1] != "http://localhost:3000" {
		t.Fatalf("origins = %#v", origins)
	}
	for _, value := range []string{
		"https://agent.example.com,https://agent.example.com",
		"https://agent.example.com/",
		"https://agent.example.com/path",
		"https://agent.example.com?query=1",
		"ftp://agent.example.com",
		"https://agent.example.com,,https://other.example.com",
	} {
		if _, err := parseMCPHTTPOrigins(value); err == nil {
			t.Fatalf("parseMCPHTTPOrigins(%q) accepted invalid origins", value)
		}
	}
}

func TestMCPHTTPExposureRequiresControlsOutsideLoopback(t *testing.T) {
	t.Parallel()
	if err := validateMCPHTTPExposure("127.0.0.1:8788", "", nil); err != nil {
		t.Fatalf("loopback exposure rejected: %v", err)
	}
	for _, test := range []struct {
		name    string
		addr    string
		token   string
		origins []string
	}{
		{name: "missing token", addr: "0.0.0.0:8788", origins: []string{"https://agent.example.com"}},
		{name: "missing origins", addr: "0.0.0.0:8788", token: "secret"},
		{name: "wildcard ipv6", addr: "[::]:8788", token: "secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateMCPHTTPExposure(test.addr, test.token, test.origins); err == nil {
				t.Fatalf("validateMCPHTTPExposure accepted unsafe config: %#v", test)
			}
		})
	}
	if err := validateMCPHTTPExposure("0.0.0.0:8788", "secret", []string{"https://agent.example.com"}); err != nil {
		t.Fatalf("explicit remote controls rejected: %v", err)
	}
}

func TestMCPHTTPAuthRejectsBeforeDispatch(t *testing.T) {
	t.Parallel()
	called := 0
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called++ })
	handler := mcpHTTPAuth("secret", next)

	for _, authorization := range []string{"", "Bearer wrong", "Bearer secret ", "Basic secret"} {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8788/mcp", nil)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("authorization %q status = %d, want 401", authorization, resp.Code)
		}
		if resp.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Fatalf("authorization %q challenge = %q", authorization, resp.Header().Get("WWW-Authenticate"))
		}
	}
	if called != 0 {
		t.Fatalf("backend called %d times for rejected auth", called)
	}

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8788/mcp", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK || called != 1 {
		t.Fatalf("valid auth status/calls = %d/%d", resp.Code, called)
	}
}

func TestMCPHTTPDefaultProfileServesOnlyRoute(t *testing.T) {
	t.Parallel()
	handler := newMCPHTTPHandler(newMCPServer("test", jinn.ShellModeDisabled), mcpHTTPConfig{addr: "127.0.0.1:8788"})
	status, body, _ := mcpHTTPPost(t, handler, "/mcp", "server/discover", "", nil)
	if status != http.StatusOK {
		t.Fatalf("discover status = %d, body = %s", status, body)
	}
	status, body, _ = mcpHTTPPost(t, handler, "/mcp", "tools/list", "", nil)
	if status != http.StatusOK {
		t.Fatalf("tools/list status = %d, body = %s", status, body)
	}
	var response struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	if len(response.Result.Tools) != 1 || response.Result.Tools[0].Name != mcpRouteTool {
		t.Fatalf("default HTTP tools = %#v", response.Result.Tools)
	}
	status, _, _ = mcpHTTPPost(t, handler, "/other", "server/discover", "", nil)
	if status != http.StatusNotFound {
		t.Fatalf("non-MCP path status = %d, want 404", status)
	}
}

func TestMCPHTTPReadOnlyProfileExecutesReadOnlyTool(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("hello over HTTP\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	engine, err := jinn.NewWithConfig(workspace, jinn.EngineConfig{Version: "test", ShellMode: jinn.ShellModeDisabled})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	defer func() { _ = engine.Close() }()
	handler := newMCPHTTPHandler(newMCPServerWithProfile("test", jinn.ShellModeUnsafe, mcpProfileReadOnly, engine), mcpHTTPConfig{addr: "127.0.0.1:8788", token: "secret"})

	headers := map[string]string{"Authorization": "Bearer secret"}
	status, body, _ := mcpHTTPPost(t, handler, "/mcp", "tools/call", `"name":"jinn_call","arguments":{"tool":"read_file","arguments":{"path":"hello.txt"},"compress":false}`, headers)
	if status != http.StatusOK {
		t.Fatalf("read_file status = %d, body = %s", status, body)
	}
	var readResponse struct {
		Result struct {
			Structured struct {
				Tool   string `json:"tool"`
				Result string `json:"result"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &readResponse); err != nil {
		t.Fatalf("decode read_file: %v", err)
	}
	if readResponse.Result.Structured.Tool != "read_file" || !strings.Contains(readResponse.Result.Structured.Result, "hello over HTTP") {
		t.Fatalf("read-only HTTP result = %#v", readResponse.Result.Structured)
	}

	status, body, _ = mcpHTTPPost(t, handler, "/mcp", "tools/call", `"name":"jinn_call","arguments":{"tool":"write_file","arguments":{"path":"blocked.txt","content":"nope"}}`, headers)
	if status != http.StatusOK || !strings.Contains(string(body), `"isError":true`) {
		t.Fatalf("write_file HTTP result status/body = %d/%s", status, body)
	}
	status, body, _ = mcpHTTPPost(t, handler, "/mcp", "tools/call", `"name":"jinn_route","arguments":{"need":"write a file","include_mutating":true}`, headers)
	if status != http.StatusOK {
		t.Fatalf("read-only route status = %d, body = %s", status, body)
	}
	var routeResponse struct {
		Result struct {
			Structured struct {
				Matches []struct {
					Mutating bool `json:"mutating"`
				} `json:"matches"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &routeResponse); err != nil {
		t.Fatalf("decode read-only route: %v", err)
	}
	for _, match := range routeResponse.Result.Structured.Matches {
		if match.Mutating {
			t.Fatalf("read-only route returned a mutating match: %#v", routeResponse.Result.Structured.Matches)
		}
	}
	status, _, _ = mcpHTTPPost(t, handler, "/mcp", "tools/call", `"name":"jinn_call","arguments":{"tool":"run_shell","arguments":{"command":"printf blocked"}}`, headers)
	if status != http.StatusOK {
		t.Fatalf("run_shell rejection status = %d", status)
	}
}

func TestMCPHTTPOriginRejectsUnlistedBrowser(t *testing.T) {
	t.Parallel()
	handler := newMCPHTTPHandler(newMCPServer("test", jinn.ShellModeDisabled), mcpHTTPConfig{
		addr:    "127.0.0.1:8788",
		origins: []string{"https://allowed.example.com"},
	})
	status, body, _ := mcpHTTPPost(t, handler, "/mcp", "server/discover", "", map[string]string{"Origin": "https://blocked.example.com"})
	if status != http.StatusForbidden || !strings.Contains(string(body), "origin not allowed") {
		t.Fatalf("blocked origin status/body = %d/%s", status, body)
	}
}

func mcpHTTPPost(t *testing.T, handler http.Handler, path, method, paramsExtra string, headers map[string]string) (int, []byte, http.Header) {
	t.Helper()
	params := "{" + mcpHTTPTestMeta
	if paramsExtra != "" {
		params += "," + paramsExtra
	}
	params += "}"
	body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":` + params + `}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8788"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", protocol.Version)
	req.Header.Set("Mcp-Method", method)
	if method == "tools/call" {
		var decoded struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(params), &decoded); err != nil {
			t.Fatalf("decode test params: %v", err)
		}
		req.Header.Set("Mcp-Name", decoded.Name)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	result, err := io.ReadAll(resp.Result().Body)
	if err != nil {
		t.Fatalf("read HTTP response: %v", err)
	}
	return resp.Code, result, resp.Header()
}
