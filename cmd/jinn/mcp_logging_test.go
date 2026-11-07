package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/voocel/mcp-sdk-go/protocol"
	"github.com/voocel/mcp-sdk-go/server"
)

type countingMCPContent struct {
	calls   *int
	payload string
	err     error
}

func (c countingMCPContent) MarshalJSON() ([]byte, error) {
	(*c.calls)++
	return []byte(c.payload), c.err
}

func TestMCPLoggingEnrichment(t *testing.T) {
	t.Parallel()
	t.Run("generic custom marshaler", func(t *testing.T) {
		t.Parallel()
		const payload = `{"route_id":"route","tool":"search","confidence":"high","score_margin":3,"error_code":"failed","retryable":true,"matches":[{"name":"first"}],"result":"{\"truncated\":true}"}`
		calls := 0
		record := mcpLogRecord{}
		enrichMCPLogRecord(&record, nil, &protocol.CallToolResult{StructuredContent: countingMCPContent{calls: &calls, payload: payload}})
		if calls != 1 || record.ResultBytes != len(payload) || record.RouteID != "route" || record.JinnTool != "search" || record.Confidence != "high" || record.ScoreMargin == nil || *record.ScoreMargin != 3 || record.ErrorCode != "failed" || record.Retryable == nil || !*record.Retryable || len(record.Recommendations) != 1 || record.Recommendations[0] != "first" || !record.Truncated {
			t.Fatalf("calls = %d, record = %+v", calls, record)
		}
	})
	t.Run("generic tags", func(t *testing.T) {
		t.Parallel()
		value := struct {
			Route string `json:"route_id"`
			Tool  string `json:"tool"`
		}{Route: "tagged", Tool: "read_file"}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		record := mcpLogRecord{}
		enrichMCPLogRecord(&record, nil, &protocol.CallToolResult{StructuredContent: value})
		if record.ResultBytes != len(data) || record.RouteID != "tagged" || record.JinnTool != "read_file" {
			t.Fatalf("record = %+v", record)
		}
	})
	t.Run("null", func(t *testing.T) {
		t.Parallel()
		calls := 0
		record := mcpLogRecord{RouteID: "existing"}
		enrichMCPLogRecord(&record, nil, &protocol.CallToolResult{StructuredContent: countingMCPContent{calls: &calls, payload: "null"}})
		// req is nil, so RouteID is reset from request params (empty) before
		// generic extraction; "null" yields no fields to restore it.
		if calls != 1 || record.ResultBytes != len("null") || record.RouteID != "" {
			t.Fatalf("calls = %d, record = %+v", calls, record)
		}
	})
	t.Run("generic marshal failure", func(t *testing.T) {
		t.Parallel()
		calls := 0
		record := mcpLogRecord{RouteID: "existing"}
		enrichMCPLogRecord(&record, nil, &protocol.CallToolResult{StructuredContent: countingMCPContent{calls: &calls, err: errors.New("cannot marshal")}})
		if calls != 1 || record.ResultBytes != 0 || record.RouteID != "" {
			t.Fatalf("calls = %d, record = %+v", calls, record)
		}
	})
	t.Run("typed marshal failure", func(t *testing.T) {
		t.Parallel()
		margin := 7
		output := jinn.RouteResponse{RouteID: "typed", Confidence: "high", ScoreMargin: &margin, Matches: []jinn.RouteMatch{{Name: "first", Schema: make(chan int)}}}
		record := mcpLogRecord{}
		enrichMCPLogRecord(&record, nil, &protocol.CallToolResult{StructuredContent: output})
		if record.ResultBytes != 0 || record.RouteID != "typed" || record.Confidence != "high" || record.ScoreMargin != &margin || len(record.Recommendations) != 1 || record.Recommendations[0] != "first" {
			t.Fatalf("record = %+v", record)
		}
	})
	t.Run("typed output", func(t *testing.T) {
		t.Parallel()
		output := mcpCallOutput{RouteID: "typed", Tool: "read_file", Result: `{"truncated_global":true}`}
		data, err := json.Marshal(output)
		if err != nil {
			t.Fatal(err)
		}
		record := mcpLogRecord{}
		enrichMCPLogRecord(&record, nil, &protocol.CallToolResult{StructuredContent: output})
		if record.ResultBytes != len(data) || record.RouteID != "typed" || record.JinnTool != "read_file" || !record.Truncated {
			t.Fatalf("record = %+v", record)
		}
	})
}

func TestMCPLoggerConfigurationAndPrivatePath(t *testing.T) {
	base := t.TempDir()
	t.Setenv("JINN_CONFIG_DIR", base)
	t.Setenv(mcpLogLevelEnv, "info")
	logger, err := newMCPLoggerFromEnv()
	if err != nil {
		t.Fatalf("newMCPLoggerFromEnv: %v", err)
	}
	logger.record(mcpLogRecord{Timestamp: "2026-01-01T00:00:00Z", Level: "info", Method: "tools/list", RequestID: "1", Outcome: "ok"})
	path := filepath.Join(base, "jinn", "logs", "mcp.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(data), "token") || !strings.Contains(string(data), `"method":"tools/list"`) {
		t.Fatalf("log data = %s", data)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("log permissions = %v, %v", info, err)
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil || dir.Mode().Perm() != 0o700 {
		t.Fatalf("log directory permissions = %v, %v", dir, err)
	}
	if _, err := parseMCPLogLevel("invalid"); err == nil {
		t.Fatal("accepted invalid log level")
	}
}

func TestMCPLoggerCapAndConcurrency(t *testing.T) {
	logger := &mcpLogger{level: mcpLogInfo, path: filepath.Join(t.TempDir(), "mcp.jsonl")}
	var group sync.WaitGroup
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			logger.record(mcpLogRecord{Timestamp: "2026-01-01T00:00:00Z", Level: "info", Method: "tools/list", Outcome: "ok"})
		}()
	}
	group.Wait()
	info, err := os.Stat(logger.path)
	if err != nil || info.Size() == 0 || info.Size() > mcpLogMaxBytes {
		t.Fatalf("log size = %v, %v", info, err)
	}
	logger.path = filepath.Join(t.TempDir(), "capped.jsonl")
	if writeErr := os.WriteFile(logger.path, make([]byte, mcpLogMaxBytes), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	logger.record(mcpLogRecord{Timestamp: "2026-01-01T00:00:00Z", Level: "info", Method: "tools/list", Outcome: "ok"})
	info, err = os.Stat(logger.path)
	if err != nil || info.Size() != mcpLogMaxBytes {
		t.Fatalf("capped log size = %v, %v", info, err)
	}
}

func TestMCPLoggerCapUsesCrossInstanceLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.jsonl")
	record := mcpLogRecord{Timestamp: "2026-01-01T00:00:00Z", Level: "info", Method: "tools/list", Outcome: "ok"}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := os.WriteFile(path, make([]byte, mcpLogMaxBytes-len(data)-1), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	first := &mcpLogger{level: mcpLogInfo, path: path}
	second := &mcpLogger{level: mcpLogInfo, path: path}
	var group sync.WaitGroup
	for _, logger := range []*mcpLogger{first, second} {
		group.Add(1)
		go func(logger *mcpLogger) { defer group.Done(); logger.record(record) }(logger)
	}
	group.Wait()
	info, err := os.Stat(path)
	if err != nil || info.Size() != mcpLogMaxBytes {
		t.Fatalf("cross-instance cap size = %v, %v", info, err)
	}
}

func TestMCPLoggerRedactsUnregisteredToolName(t *testing.T) {
	logger := &mcpLogger{level: mcpLogInfo, path: filepath.Join(t.TempDir(), "mcp.jsonl")}
	srv := newMCPServerWithProfileAndLogger("test", jinn.ShellModeDisabled, mcpProfileDiscover, nil, logger)
	valid := handleMCPServerTestLine(t, srv, `{"jsonrpc":"2.0","id":"valid","method":"tools/call","params":{`+currentMCPMeta+`,"name":"jinn_route","arguments":{"need":"read source"}}}`)
	if valid["error"] != nil {
		t.Fatalf("valid call failed: %#v", valid)
	}
	_ = handleMCPServerTestLine(t, srv, `{"jsonrpc":"2.0","id":"sentinel","method":"tools/call","params":{`+currentMCPMeta+`,"name":"https://token@example.test/private","arguments":{}}}`)
	data, err := os.ReadFile(logger.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "token@example.test") || strings.Contains(string(data), "private") {
		t.Fatalf("unregistered tool leaked into log: %s", data)
	}
	if !strings.Contains(string(data), `"toolName":"jinn_route"`) {
		t.Fatalf("registered tool missing from log: %s", data)
	}
}

func TestMCPRecoveryMiddlewareHandlesNilRequest(t *testing.T) {
	t.Parallel()
	handler := mcpRecoveryMiddleware(&mcpLogger{level: mcpLogOff})(func(context.Context, *server.Request) (protocol.Result, error) {
		panic("sentinel")
	})
	result, err := handler(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "internal error") {
		t.Fatalf("recovered error = %v", err)
	}
	if result != nil {
		t.Fatalf("recovered result = %#v, want nil", result)
	}
}
