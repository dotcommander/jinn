package mcpexplore

import (
	"context"
	"errors"
	"os"
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
	got := CommandEnvironment([]string{"KEEP=one", HTTPTokenEnv + "=secret", "OTHER=two"})
	if strings.Join(got, "\n") != "KEEP=one\nOTHER=two" {
		t.Fatalf("CommandEnvironment = %#v", got)
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
