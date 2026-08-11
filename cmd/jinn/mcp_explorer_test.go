package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/jinn/internal/mcpexplore"
	"github.com/voocel/mcp-sdk-go/protocol"
)

func TestParseMCPExplorerHTTPAndTypedArguments(t *testing.T) {
	config, err := parseMCPExplorer([]string{"call", "https://mcp.example.test/mcp", "tool", "--args", `{"count":1,"old":"value"}`, "-a", "count", "2", "--argument", "enabled", "true", "-a", "label", "plain", "--timeout", "5s"})
	if err != nil {
		t.Fatalf("parseMCPExplorer: %v", err)
	}
	if config.timeout != 5*time.Second || config.tool != "tool" {
		t.Fatalf("parsed config = %#v", config)
	}
	if got := config.arguments["count"]; got != json.Number("2") {
		t.Fatalf("later numeric assignment = %#v", got)
	}
	if got := config.arguments["enabled"]; got != true {
		t.Fatalf("boolean assignment = %#v", got)
	}
	if got := config.arguments["label"]; got != "plain" {
		t.Fatalf("string assignment = %#v", got)
	}
}

func TestParseMCPExplorerCommandHasExplicitArgv(t *testing.T) {
	config, err := parseMCPExplorer([]string{"inspect", "--command", "/tmp/mcp-server", "--arg", "--profile", "--arg", "read-only", "jinn_route"})
	if err != nil {
		t.Fatalf("parseMCPExplorer: %v", err)
	}
	if config.target.Command != "/tmp/mcp-server" || config.tool != "jinn_route" {
		t.Fatalf("parsed config = %#v", config)
	}
	if got := len(config.target.CommandArgs); got != 2 || config.target.CommandArgs[0] != "--profile" || config.target.CommandArgs[1] != "read-only" {
		t.Fatalf("command argv = %#v", config.target.CommandArgs)
	}
}

func TestParseMCPExplorerErrorContracts(t *testing.T) {
	tests := []struct {
		name                string
		args                []string
		wantError           string
		wantAction          string
		wantEndpoint        string
		wantCommand         string
		wantTimeout         time.Duration
		wantCommandArgCount int
		wantArgumentCount   int
	}{
		{
			name:                "missing option value preserves prior command",
			args:                []string{"call", "--command", "/tmp/mcp-server", "--arg"},
			wantError:           "--arg requires a value",
			wantAction:          "call",
			wantCommand:         "/tmp/mcp-server",
			wantTimeout:         mcpExplorerDefaultTimeout,
			wantCommandArgCount: 0,
			wantArgumentCount:   0,
		},
		{
			name:                "command argument validation precedes call argument validation",
			args:                []string{"list", "--arg", "profile", "--args", `{"mode":"safe"}`},
			wantError:           "--arg requires --command",
			wantAction:          "list",
			wantTimeout:         mcpExplorerDefaultTimeout,
			wantCommandArgCount: 1,
			wantArgumentCount:   1,
		},
		{
			name:              "invalid endpoint preserves prior timeout",
			args:              []string{"call", "--timeout", "5s", "file:///tmp/mcp", "tool"},
			wantError:         `invalid MCP endpoint "file:///tmp/mcp": use an http:// or https:// URL`,
			wantAction:        "call",
			wantEndpoint:      "file:///tmp/mcp",
			wantTimeout:       5 * time.Second,
			wantArgumentCount: 0,
		},
		{
			name:              "missing tool",
			args:              []string{"inspect", "https://mcp.example.test/mcp"},
			wantError:         "mcp inspect requires exactly one TOOL",
			wantAction:        "inspect",
			wantEndpoint:      "https://mcp.example.test/mcp",
			wantTimeout:       mcpExplorerDefaultTimeout,
			wantArgumentCount: 0,
		},
		{
			name:              "extra tool positional",
			args:              []string{"call", "https://mcp.example.test/mcp", "tool", "extra"},
			wantError:         "mcp call requires exactly one TOOL",
			wantAction:        "call",
			wantEndpoint:      "https://mcp.example.test/mcp",
			wantTimeout:       mcpExplorerDefaultTimeout,
			wantArgumentCount: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := parseMCPExplorer(test.args)
			if err == nil || err.Error() != test.wantError {
				t.Fatalf("parseMCPExplorer(%q) error = %v, want %q", test.args, err, test.wantError)
			}
			if config.action != test.wantAction || config.target.Endpoint != test.wantEndpoint || config.target.Command != test.wantCommand || config.timeout != test.wantTimeout || len(config.target.CommandArgs) != test.wantCommandArgCount || len(config.arguments) != test.wantArgumentCount {
				t.Fatalf("parseMCPExplorer(%q) config = %#v", test.args, config)
			}
		})
	}
}

func TestParseMCPExplorerRejectsInvalidTransport(t *testing.T) {
	for _, args := range [][]string{
		{"list", "file:///tmp/mcp"},
		{"list", "--command", "/tmp/mcp", "https://mcp.example.test/mcp"},
		{"call", "https://mcp.example.test/mcp", "tool", "--args", "[]"},
	} {
		if _, err := parseMCPExplorer(args); err == nil {
			t.Fatalf("parseMCPExplorer(%q) accepted invalid input", args)
		}
	}
}

func TestParseMCPExplorerRejectsActionIncompatibleFlags(t *testing.T) {
	for _, args := range [][]string{
		{"list", "https://mcp.example.test/mcp", "--args", `{}`},
		{"inspect", "https://mcp.example.test/mcp", "tool", "-a", "name", "value"},
		{"inspect", "https://mcp.example.test/mcp", "tool", "--format=signatures"},
		{"list", "https://mcp.example.test/mcp", "--encoding=cl100k_base"},
		{"cost", "https://mcp.example.test/mcp", "--format=signatures"},
		{"list", "https://mcp.example.test/mcp", "--arg", "--profile"},
		{"list", "--command", "/tmp/mcp", "--token", "secret"},
	} {
		if _, err := parseMCPExplorer(args); err == nil {
			t.Fatalf("parseMCPExplorer(%q) accepted incompatible flags", args)
		}
	}
}

func TestMCPExplorerTokenIsEnvironmentOnly(t *testing.T) {
	const sentinel = "do-not-render-this-token"
	for _, args := range [][]string{
		{"list", "https://mcp.example.test/mcp", "--token", sentinel},
		{"list", "https://mcp.example.test/mcp", "--token=" + sentinel},
	} {
		_, err := parseMCPExplorer(args)
		if err == nil {
			t.Fatalf("parseMCPExplorer(%q) accepted --token", args)
		}
		if err.Error() != mcpExplorerTokenOptionError || strings.Contains(err.Error(), sentinel) {
			t.Fatalf("parseMCPExplorer(%q) error = %q", args, err)
		}
		if err := runMCPExplorer(context.Background(), args); err == nil || strings.Contains(err.Error(), sentinel) {
			t.Fatalf("runMCPExplorer(%q) error = %v", args, err)
		}
	}
	if strings.Contains(mcpExplorerHelp, " --token ") || strings.Contains(mcpExplorerHelp, "--token=") || strings.Contains(mcpExplorerHelp, sentinel) {
		t.Fatal("MCP explorer help advertises or renders --token")
	}
}

func TestMCPExplorerEndpointRejectsEmbeddedCredentialsWithoutRenderingThem(t *testing.T) {
	const username = "do-not-render-user"
	const password = "do-not-render-password"
	_, err := parseMCPExplorer([]string{"list", "https://" + username + ":" + password + "@mcp.example.test/mcp"})
	if err == nil {
		t.Fatal("parseMCPExplorer accepted endpoint credentials")
	}
	if err.Error() != "MCP endpoint must not include embedded credentials" || strings.Contains(err.Error(), username) || strings.Contains(err.Error(), password) {
		t.Fatalf("credential rejection error = %q", err)
	}
}

func TestMCPExplorerCommandEnvRemovesHTTPToken(t *testing.T) {
	const token = "subprocess-token"
	t.Setenv(mcpexplore.HTTPTokenEnv, token)
	command := mcpexplore.NewCommand("/bin/echo")
	for _, entry := range command.Env {
		if entry == mcpexplore.HTTPTokenEnv+"="+token {
			t.Fatalf("subprocess environment retained %s", mcpexplore.HTTPTokenEnv)
		}
	}
	got := mcpexplore.CommandEnvironment([]string{"KEEP=one", mcpexplore.HTTPTokenEnv + "=" + token, "OTHER=two"})
	if strings.Join(got, "\n") != "KEEP=one\nOTHER=two" {
		t.Fatalf("filtered environment = %#v", got)
	}
}

func TestMCPExplorerHTTPAuthorizationTrimsTokenWhitespace(t *testing.T) {
	t.Setenv(mcpexplore.HTTPTokenEnv, " \ttrimmed-token\n")
	gotAuthorization := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	httpClient, err := mcpexplore.NewHTTPClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response, err := httpClient.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if got := <-gotAuthorization; got != "Bearer trimmed-token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestMCPExplorerHTTPAuthorizationStaysOnOriginalOrigin(t *testing.T) {
	const token = "do-not-leak"
	t.Setenv(mcpexplore.HTTPTokenEnv, token)
	destinationAuth := make(chan string, 1)
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationAuth <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	originalAuth := make(chan string, 1)
	original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originalAuth <- r.Header.Get("Authorization")
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer original.Close()

	httpClient, err := mcpexplore.NewHTTPClient(original.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = httpClient.Get(original.URL)
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("cross-origin redirect error = %v", err)
	}
	if got := <-originalAuth; got != "Bearer "+token {
		t.Fatalf("original authorization = %q", got)
	}
	select {
	case got := <-destinationAuth:
		t.Fatalf("redirect destination received authorization %q", got)
	default:
	}
}

func TestMCPExplorerHTTPAuthorizationAllowsSameOriginRedirect(t *testing.T) {
	const token = "same-origin-token"
	t.Setenv(mcpexplore.HTTPTokenEnv, token)
	authError := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			authError <- got
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	httpClient, err := mcpexplore.NewHTTPClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response, err := httpClient.Get(server.URL)
	if err != nil {
		t.Fatalf("same-origin redirect: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != http.StatusOK {
		t.Fatalf("same-origin status = %d", response.StatusCode)
	}
	select {
	case got := <-authError:
		t.Fatalf("same-origin authorization = %q", got)
	default:
	}
}

func TestMCPExplorerSignatureListOutputIsDeterministic(t *testing.T) {
	server := &protocol.Implementation{Name: "example", Version: "1.0"}
	tools := []*protocol.Tool{
		{Name: "zeta", Description: "Z", InputSchema: protocol.JSONSchema{"type": "object", "properties": map[string]any{}}},
		{Name: "alpha", Description: "A", InputSchema: protocol.JSONSchema{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []any{"query"}, "additionalProperties": false}},
	}
	rendered, err := renderMCPExplorerJSON(newMCPExplorerSignaturesOutput(server, tools))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"server\":{\"name\":\"example\",\"version\":\"1.0\"},\"tools\":[{\"signature\":\"alpha(query:string)\",\"description\":\"A\"},{\"signature\":\"zeta(...:json)\",\"description\":\"Z\"}]}\n"
	if string(rendered) != want {
		t.Fatalf("signature output = %s, want %s", rendered, want)
	}
}
