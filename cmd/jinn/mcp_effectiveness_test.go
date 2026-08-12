package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/voocel/mcp-sdk-go/protocol"
)

func TestMCPRouteReturnsAdaptiveExecutionPacket(t *testing.T) {
	t.Parallel()
	srv := newMCPServerWithProfile("test", jinn.ShellModeDisabled, mcpProfileDiscover, nil)
	response := handleMCPServerTestLine(t, srv, mcpToolCallRequest(t, "route", mcpRouteTool, map[string]any{
		"need": "read a file", "include_call": true,
	}))
	result := response["result"].(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	routeID, _ := structured["route_id"].(string)
	if len(routeID) != 32 || structured["adaptive"] != true || structured["confidence"] != "high" {
		t.Fatalf("route metadata = %#v", structured)
	}
	matches := structured["matches"].([]any)
	call := matches[0].(map[string]any)["call"].(map[string]any)
	if call["tool"] != "read_file" || call["arguments"].(map[string]any)["path"] != "<required>" {
		t.Fatalf("call template = %#v", call)
	}
}

func TestMCPCallPreservesStructuredRecovery(t *testing.T) {
	t.Parallel()
	routeID := strings.Repeat("a", 32)
	engine, _ := newMCPTestEngine(t)
	srv := newMCPServerWithProfile("test", jinn.ShellModeDisabled, mcpProfileReadOnly, engine)
	response := handleMCPServerTestLine(t, srv, mcpToolCallRequest(t, "call", mcpCallTool, map[string]any{
		"tool": "read_file", "route_id": routeID, "arguments": map[string]any{"path": "missing.txt"},
	}))
	result := response["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("call did not return an MCP tool error: %#v", result)
	}
	structured := result["structuredContent"].(map[string]any)
	if structured["error_code"] != jinn.ErrCodeFileNotFound || structured["route_id"] != routeID || structured["retryable"] != true {
		t.Fatalf("structured recovery = %#v", structured)
	}
	next := structured["next_call"].(map[string]any)
	if next["tool"] != "list_dir" || next["arguments"].(map[string]any)["path"] != "." {
		t.Fatalf("next_call = %#v", next)
	}
}

func TestMCPCallRejectsUntrustedRouteID(t *testing.T) {
	t.Parallel()
	engine, _ := newMCPTestEngine(t)
	srv := newMCPServerWithProfile("test", jinn.ShellModeDisabled, mcpProfileReadOnly, engine)
	response := handleMCPServerTestLine(t, srv, mcpToolCallRequest(t, "call", mcpCallTool, map[string]any{
		"tool": "read_file", "route_id": "private.txt", "arguments": map[string]any{"path": "missing.txt"},
	}))
	structured := response["result"].(map[string]any)["structuredContent"].(map[string]any)
	if structured["error_code"] != jinn.ErrCodeInvalidArgs {
		t.Fatalf("invalid route response = %#v", structured)
	}
	if _, ok := structured["route_id"]; ok {
		t.Fatalf("untrusted route_id was echoed: %#v", structured)
	}
}

func TestMCPLogLinksRouteToOutcomeWithoutContent(t *testing.T) {
	t.Parallel()
	engine, workspace := newMCPTestEngine(t)
	if err := os.WriteFile(filepath.Join(workspace, "private.txt"), []byte("secret-needle\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	logger := &mcpLogger{level: mcpLogInfo, path: filepath.Join(t.TempDir(), "mcp.jsonl")}
	srv := newMCPServerWithProfileAndLogger("test", jinn.ShellModeDisabled, mcpProfileReadOnly, engine, logger)
	routeResponse := handleMCPServerTestLine(t, srv, mcpToolCallRequest(t, "route", mcpRouteTool, map[string]any{"need": "read a file"}))
	route := routeResponse["result"].(map[string]any)["structuredContent"].(map[string]any)
	routeID := route["route_id"].(string)
	_ = handleMCPServerTestLine(t, srv, mcpToolCallRequest(t, "call", mcpCallTool, map[string]any{
		"tool": "read_file", "route_id": routeID, "arguments": map[string]any{"path": "private.txt"}, "compress": false,
	}))
	data, err := os.ReadFile(logger.path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	text := string(data)
	for _, secret := range []string{"read a file", "private.txt", "secret-needle"} {
		if strings.Contains(text, secret) {
			t.Fatalf("log leaked %q: %s", secret, text)
		}
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 2 {
		t.Fatalf("log record count = %d: %s", len(lines), text)
	}
	var routeRecord, callRecord mcpLogRecord
	if err := json.Unmarshal([]byte(lines[0]), &routeRecord); err != nil {
		t.Fatalf("decode route record: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &callRecord); err != nil {
		t.Fatalf("decode call record: %v", err)
	}
	if routeRecord.RouteID != routeID || callRecord.RouteID != routeID || callRecord.JinnTool != "read_file" || len(routeRecord.Recommendations) == 0 || routeRecord.V != 2 || callRecord.V != 2 {
		t.Fatalf("linked records = route:%+v call:%+v", routeRecord, callRecord)
	}
	info, err := os.Stat(logger.path)
	if err != nil {
		t.Fatalf("stat log: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("log mode = %o, want 600", info.Mode().Perm())
	}

	legacyData, err := json.Marshal(mcpLogRecord{
		Timestamp: "2026-08-12T00:00:00Z", Level: "info", Method: "tools/call",
		RequestID: "legacy", Outcome: "ok", ToolName: mcpRouteTool,
	})
	if err != nil {
		t.Fatalf("marshal legacy record: %v", err)
	}
	mixed := append(append(legacyData, '\n'), data...)
	mixedLines := strings.Split(strings.TrimSpace(string(mixed)), "\n")
	if len(mixedLines) != 3 {
		t.Fatalf("mixed log record count = %d", len(mixedLines))
	}
	for index, line := range mixedLines {
		var record mcpLogRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode mixed record %d: %v", index, err)
		}
		if index == 0 && (record.V != 0 || record.RequestID != "legacy") {
			t.Fatalf("legacy record changed: %+v", record)
		}
		if index > 0 && record.V != 2 {
			t.Fatalf("additive record %d version = %d", index, record.V)
		}
	}
}

func newMCPTestEngine(t *testing.T) (*jinn.Engine, string) {
	t.Helper()
	workspace := t.TempDir()
	engine, err := jinn.NewWithConfig(workspace, jinn.EngineConfig{Version: "test", ShellMode: jinn.ShellModeDisabled})
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine, workspace
}

func mcpToolCallRequest(t *testing.T, id, name string, arguments map[string]any) string {
	t.Helper()
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"_meta": map[string]any{
				"io.modelcontextprotocol/protocolVersion":    protocol.Version,
				"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			},
			"name": name, "arguments": arguments,
		},
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return string(data)
}
