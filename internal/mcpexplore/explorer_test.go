package mcpexplore

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestTargetValidate(t *testing.T) {
	tests := []struct {
		target Target
		want   string
	}{
		{target: Target{Endpoint: "https://mcp.example.test/mcp"}},
		{target: Target{Command: "/tmp/mcp", CommandArgs: []string{"--read-only"}}},
		{target: Target{Endpoint: "file:///tmp/mcp"}, want: `invalid MCP endpoint "file:///tmp/mcp": use an http:// or https:// URL`},
		{target: Target{Endpoint: "https://user:pass@mcp.example.test/mcp"}, want: "MCP endpoint must not include embedded credentials"},
	}
	for _, tt := range tests {
		err := tt.target.Validate()
		if tt.want == "" && err != nil {
			t.Fatalf("Validate(%+v): %v", tt.target, err)
		}
		if tt.want != "" && (err == nil || err.Error() != tt.want) {
			t.Fatalf("Validate(%+v) = %v, want %q", tt.target, err, tt.want)
		}
	}
}

func TestCommandEnvironmentRemovesToken(t *testing.T) {
	t.Parallel()
	got := CommandEnvironment([]string{"KEEP=one", HTTPTokenEnv + "=secret", "OTHER=two"})
	if strings.Join(got, "\n") != "KEEP=one\nOTHER=two" {
		t.Fatalf("CommandEnvironment = %#v", got)
	}
}

func TestCommandEnvironmentPlatformSemantics(t *testing.T) {
	t.Parallel()
	environ := []string{"KEEP=one", "jinn_mcp_http_token=mixed", HTTPTokenEnv + "=exact", "OTHER=two"}
	for _, test := range []struct {
		name      string
		normalize func(string) string
		want      string
	}{
		{"windows", strings.ToUpper, "KEEP=one\nOTHER=two"},
		{"posix", func(name string) string { return name }, "KEEP=one\njinn_mcp_http_token=mixed\nOTHER=two"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := strings.Join(commandEnvironment(environ, test.normalize), "\n"); got != test.want {
				t.Fatalf("commandEnvironment = %q, want %q", got, test.want)
			}
		})
	}
	want := "KEEP=one\njinn_mcp_http_token=mixed\nOTHER=two"
	if runtime.GOOS == "windows" {
		want = "KEEP=one\nOTHER=two"
	}
	if got := strings.Join(CommandEnvironment(environ), "\n"); got != want {
		t.Fatalf("CommandEnvironment = %q, want %q", got, want)
	}
}

func TestTargetValidateDoesNotExposeMalformedCredentials(t *testing.T) {
	const secret = "do-not-render-this-secret"
	err := (Target{Endpoint: "https://user:" + secret + "@[::1"}).Validate()
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("credential-bearing parse error = %v", err)
	}
}

func TestNewCommandContextRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := NewCommandContext(ctx, executable, "-test.run=^$")
	if err := command.Start(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start() error = %v, want context.Canceled", err)
	}
}
