package jinn

import (
	"context"
	"encoding/json"
	"testing"
)

func listToolsCaps(t *testing.T, args map[string]any) ToolCapabilities {
	t.Helper()
	engine := New(t.TempDir(), "test")
	result, _, err := engine.Dispatch(context.Background(), "list_tools", args)
	if err != nil {
		t.Fatalf("Dispatch list_tools: %v", err)
	}
	var caps ToolCapabilities
	if err := json.Unmarshal([]byte(result.Text), &caps); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	return caps
}

func TestListToolsRiskFilter(t *testing.T) {
	t.Parallel()
	caps := listToolsCaps(t, map[string]any{"risk": "read_only"})
	if len(caps.Tools) == 0 {
		t.Fatal("read_only filter returned no tools")
	}
	for _, name := range caps.Tools {
		descriptor, ok := lookupToolDescriptor(name)
		if !ok || descriptor.routeRisk != toolRouteRiskReadOnly {
			t.Fatalf("tool %q is not read_only: %+v", name, descriptor)
		}
	}
	if _, ok := caps.Features["run_shell"]; ok {
		t.Fatal("features include filtered-out run_shell")
	}
}

func TestListToolsNetworkFilter(t *testing.T) {
	t.Parallel()
	caps := listToolsCaps(t, map[string]any{"risk": "network"})
	if len(caps.Tools) != 2 || caps.Tools[0] != webFetchTool || caps.Tools[1] != webSearchTool {
		t.Fatalf("network tools = %v", caps.Tools)
	}
}

func TestListToolsNamesAndQuery(t *testing.T) {
	t.Parallel()
	caps := listToolsCaps(t, map[string]any{"names": []any{"read_file", "list_tools"}})
	if len(caps.Tools) != 2 || caps.Tools[0] != "read_file" || caps.Tools[1] != "list_tools" {
		t.Fatalf("names filter = %v", caps.Tools)
	}

	caps = listToolsCaps(t, map[string]any{"query": "dry_run"})
	found := false
	for _, name := range caps.Tools {
		if name == "write_file" || name == "edit_file" {
			found = true
		}
	}
	if !found {
		t.Fatalf("query dry_run = %v, want write_file or edit_file", caps.Tools)
	}
}

func TestListToolsUnknownNameAndRisk(t *testing.T) {
	t.Parallel()
	engine := New(t.TempDir(), "test")
	if _, _, err := engine.Dispatch(context.Background(), "list_tools", map[string]any{"names": []any{"nosuch"}}); err == nil {
		t.Fatal("unknown name accepted")
	}
	if _, _, err := engine.Dispatch(context.Background(), "list_tools", map[string]any{"risk": "dangerous"}); err == nil {
		t.Fatal("invalid risk accepted")
	}
}

func TestListToolsDescribe(t *testing.T) {
	t.Parallel()
	caps := listToolsCaps(t, map[string]any{"describe": true, "names": []any{"read_file"}})
	if len(caps.Descriptions) != 1 {
		t.Fatalf("descriptions = %v", caps.Descriptions)
	}
	if caps.Descriptions["read_file"] == "" {
		t.Fatal("read_file description empty")
	}
	if _, ok := caps.Features["list_tools"]; ok {
		t.Fatal("features not filtered to named tools")
	}
}

func TestListToolsDefaultUnchanged(t *testing.T) {
	t.Parallel()
	engine := New(t.TempDir(), "test")
	caps := listToolsCaps(t, map[string]any{})
	if len(caps.Tools) != len(engine.registeredToolNames()) {
		t.Fatalf("default tools = %d, registered = %d", len(caps.Tools), len(engine.registeredToolNames()))
	}
	if len(caps.Descriptions) != 0 {
		t.Fatalf("descriptions present without describe: %v", caps.Descriptions)
	}
}
