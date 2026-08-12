package main

import (
	"strings"
	"testing"

	"github.com/dotcommander/jinn/internal/jinn"
)

func TestMCPNetworkProfileAllowlistAndCompressionDefaults(t *testing.T) {
	t.Parallel()
	schema := mcpCallInputSchemaForProfile(mcpProfileNetwork)
	properties := schema["properties"].(map[string]any)
	enum := properties["tool"].(map[string]any)["enum"].([]string)
	seen := make(map[string]bool, len(enum))
	for _, name := range enum {
		seen[name] = true
	}
	if !seen["web_fetch"] || !seen["web_search"] {
		t.Fatalf("network enum = %v", enum)
	}
	if seen["write_file"] || seen["run_shell"] || seen["memory"] {
		t.Fatalf("network enum leaked non-read-only tool: %v", enum)
	}
	compress := properties["compress"].(map[string]any)
	if _, ok := compress["default"]; ok {
		t.Fatalf("network compression schema must not advertise one default: %#v", compress)
	}
	if got := compress["description"].(string); got == "" {
		t.Fatal("network compression schema description is empty")
	}
	falseValue, trueValue := false, true
	for _, test := range []struct {
		tool     string
		explicit *bool
		want     bool
	}{
		{tool: "read_file", want: true},
		{tool: "web_fetch", want: false},
		{tool: "web_search", want: false},
		{tool: "web_fetch", explicit: &trueValue, want: true},
		{tool: "read_file", explicit: &falseValue, want: false},
	} {
		if got := mcpCallCompressionEnabled(test.tool, test.explicit); got != test.want {
			t.Errorf("compression(%q, %v) = %t, want %t", test.tool, test.explicit, got, test.want)
		}
	}

	route := mcpRouteInputSchemaForProfile(mcpProfileReadOnly)
	readOnlyProperties := route["properties"].(map[string]any)
	if got := readOnlyProperties["include_network"].(map[string]any)["default"]; got != false {
		t.Fatalf("read-only include_network default = %#v", got)
	}
}

func TestMCPNetworkCallIsOpenWorld(t *testing.T) {
	t.Parallel()
	engine, err := jinn.NewWithConfig(t.TempDir(), jinn.EngineConfig{Version: "test", ShellMode: jinn.ShellModeDisabled})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	resp := handleMCPServerTestLine(t, newMCPServerWithProfile("test", jinn.ShellModeDisabled, mcpProfileNetwork, engine), `{"jsonrpc":"2.0","id":"tools","method":"tools/list","params":{`+currentMCPMeta+`}}`)
	tools := resp["result"].(map[string]any)["tools"].([]any)
	for _, raw := range tools {
		tool := raw.(map[string]any)
		if tool["name"] == mcpCallTool {
			annotations := tool["annotations"].(map[string]any)
			if annotations["openWorldHint"] != true {
				t.Fatalf("network jinn_call annotations = %#v", annotations)
			}
			if description, _ := tool["description"].(string); !strings.Contains(description, "Call jinn_route first") || !strings.Contains(description, "leave this machine") || !strings.Contains(description, "provider quota") {
				t.Fatalf("network jinn_call warning = %q", description)
			}
			return
		}
	}
	t.Fatal("network profile omitted jinn_call")
}

func TestLegacyMCPIsDiscoverOnly(t *testing.T) {
	t.Parallel()
	if !allowsLegacyMCP(mcpProfileDiscover) {
		t.Fatal("discover profile lost legacy compatibility")
	}
	if allowsLegacyMCP(mcpProfileReadOnly) || allowsLegacyMCP(mcpProfileNetwork) {
		t.Fatal("opt-in execution profile enabled legacy compatibility")
	}
}
