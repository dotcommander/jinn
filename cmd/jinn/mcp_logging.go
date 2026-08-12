package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/voocel/mcp-sdk-go/protocol"
	"github.com/voocel/mcp-sdk-go/server"
)

const (
	mcpLogLevelEnv = "JINN_MCP_LOG_LEVEL"
	mcpLogMaxBytes = 10 << 20
)

type mcpLogLevel string

const (
	mcpLogOff   mcpLogLevel = "off"
	mcpLogError mcpLogLevel = "error"
	mcpLogInfo  mcpLogLevel = "info"
	mcpLogDebug mcpLogLevel = "debug"
)

// mcpLogger deliberately owns no stdout/stderr sink: MCP transports use those
// streams as protocol data. It is best effort and failures are never surfaced
// to a client request.
type mcpLogger struct {
	level mcpLogLevel
	path  string
	mu    sync.Mutex
}

type mcpLogRecord struct {
	V               int      `json:"v,omitempty"`
	Timestamp       string   `json:"timestamp"`
	Level           string   `json:"level"`
	Method          string   `json:"method"`
	RequestID       string   `json:"requestId"`
	Duration        int64    `json:"durationMs"`
	Outcome         string   `json:"outcome"`
	ToolName        string   `json:"toolName,omitempty"`
	ErrorCode       string   `json:"errorCode,omitempty"`
	RouteID         string   `json:"routeId,omitempty"`
	JinnTool        string   `json:"jinnTool,omitempty"`
	Recommendations []string `json:"recommendations,omitempty"`
	Confidence      string   `json:"confidence,omitempty"`
	ScoreMargin     *int     `json:"scoreMargin,omitempty"`
	ResultBytes     int      `json:"resultBytes,omitempty"`
	Truncated       bool     `json:"truncated,omitempty"`
	Retryable       *bool    `json:"retryable,omitempty"`
}

func newMCPLoggerFromEnv() (*mcpLogger, error) {
	level, err := parseMCPLogLevel(os.Getenv(mcpLogLevelEnv))
	if err != nil || level == mcpLogOff {
		return nil, err
	}
	path, err := mcpLogFilePath()
	if err != nil {
		return nil, err
	}
	return &mcpLogger{level: level, path: path}, nil
}

func parseMCPLogLevel(value string) (mcpLogLevel, error) {
	switch mcpLogLevel(strings.ToLower(strings.TrimSpace(value))) {
	case "", mcpLogOff:
		return mcpLogOff, nil
	case mcpLogError, mcpLogInfo, mcpLogDebug:
		return mcpLogLevel(strings.ToLower(strings.TrimSpace(value))), nil
	default:
		return "", fmt.Errorf("invalid %s %q: use off, error, info, or debug", mcpLogLevelEnv, value)
	}
}

func mcpLogFilePath() (string, error) {
	base := strings.TrimSpace(os.Getenv("JINN_CONFIG_DIR"))
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("MCP log: resolve config dir: %w", err)
		}
	}
	return filepath.Join(base, "jinn", "logs", "mcp.jsonl"), nil
}

func (l *mcpLogger) enabled(level mcpLogLevel) bool {
	if l == nil || l.level == mcpLogOff {
		return false
	}
	return l.level == mcpLogDebug || l.level == mcpLogInfo || level == mcpLogError
}

func (l *mcpLogger) record(record mcpLogRecord) {
	if !l.enabled(mcpLogLevel(record.Level)) {
		return
	}
	data, err := json.Marshal(record)
	if err != nil || len(data)+1 > mcpLogMaxBytes {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // enforce private log directory.
		return
	}
	_ = withMCPLogLock(l.path+".lock", func() error {
		f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		if chmodErr := f.Chmod(0o600); chmodErr != nil { //nolint:gosec // enforce private log file.
			return chmodErr
		}
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if info.Size()+int64(len(data)+1) > mcpLogMaxBytes {
			return nil
		}
		_, err = f.Write(append(data, '\n'))
		return err
	})
}

func mcpRecoveryMiddleware(logger *mcpLogger) server.Middleware {
	return func(next server.RawHandler) server.RawHandler {
		return func(ctx context.Context, req *server.Request) (result protocol.Result, err error) {
			defer func() {
				if recover() != nil {
					if logger != nil {
						method, requestID := mcpRequestMetadata(req)
						logger.record(mcpLogRecord{V: 2, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Level: string(mcpLogError), Method: method, RequestID: requestID, Outcome: "panic", ErrorCode: "internal_error"})
					}
					result, err = nil, protocol.Errorf(protocol.CodeInternal, "internal error")
				}
			}()
			return next(ctx, req)
		}
	}
}

//nolint:goconst // Outcome strings are intentionally local structured-log values.
func mcpLoggingMiddleware(logger *mcpLogger) server.Middleware {
	return func(next server.RawHandler) server.RawHandler {
		return func(ctx context.Context, req *server.Request) (protocol.Result, error) {
			started := time.Now()
			result, err := next(ctx, req)
			if logger != nil {
				method, requestID := mcpRequestMetadata(req)
				record := mcpLogRecord{V: 2, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Level: string(mcpLogInfo), Method: method, RequestID: requestID, Duration: time.Since(started).Milliseconds(), Outcome: "ok", ToolName: mcpRequestToolName(req)}
				enrichMCPLogRecord(&record, req, result)
				if err != nil {
					record.Outcome, record.ErrorCode, record.Level = "error", "protocol_error", string(mcpLogError)
				} else if call, ok := result.(*protocol.CallToolResult); ok && call.IsError {
					record.Outcome, record.Level = "tool_error", string(mcpLogError)
					if record.ErrorCode == "" {
						record.ErrorCode = "tool_error"
					}
				}
				logger.record(record)
			}
			return result, err
		}
	}
}

func enrichMCPLogRecord(record *mcpLogRecord, req *server.Request, result protocol.Result) {
	var params struct {
		Name      string `json:"name"`
		Arguments struct {
			RouteID string `json:"route_id"`
			Tool    string `json:"tool"`
		} `json:"arguments"`
	}
	if req != nil {
		_ = json.Unmarshal(req.RawParams(), &params)
	}
	record.RouteID = params.Arguments.RouteID
	record.JinnTool = params.Arguments.Tool
	call, ok := result.(*protocol.CallToolResult)
	if !ok || call.StructuredContent == nil {
		return
	}
	if data, err := json.Marshal(call.StructuredContent); err == nil {
		record.ResultBytes = len(data)
	}
	switch output := call.StructuredContent.(type) {
	case jinn.RouteResponse:
		record.RouteID = output.RouteID
		record.Confidence = output.Confidence
		record.ScoreMargin = output.ScoreMargin
		record.Recommendations = make([]string, 0, len(output.Matches))
		for _, match := range output.Matches {
			record.Recommendations = append(record.Recommendations, match.Name)
		}
	case mcpCallOutput:
		record.RouteID = output.RouteID
		record.JinnTool = output.Tool
		record.Truncated = mcpOutputTruncated(output.Meta, output.Result)
	case mcpCallErrorOutput:
		record.RouteID = output.RouteID
		record.JinnTool = output.Tool
		record.ErrorCode = output.ErrorCode
		retryable := output.Retryable
		record.Retryable = &retryable
	default:
		enrichMCPLogMap(record, call.StructuredContent)
	}
}

func enrichMCPLogMap(record *mcpLogRecord, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	var object map[string]any
	if json.Unmarshal(data, &object) != nil {
		return
	}
	if text, ok := object["route_id"].(string); ok {
		record.RouteID = text
	}
	if text, ok := object["tool"].(string); ok {
		record.JinnTool = text
	}
	if text, ok := object["confidence"].(string); ok {
		record.Confidence = text
	}
	if number, ok := object["score_margin"].(float64); ok {
		margin := int(number)
		record.ScoreMargin = &margin
	}
	if text, ok := object["error_code"].(string); ok {
		record.ErrorCode = text
	}
	if flag, ok := object["retryable"].(bool); ok {
		record.Retryable = &flag
	}
	if matches, ok := object["matches"].([]any); ok {
		for _, raw := range matches {
			match, _ := raw.(map[string]any)
			if name, ok := match["name"].(string); ok {
				record.Recommendations = append(record.Recommendations, name)
			}
		}
	}
	if result, ok := object["result"].(string); ok {
		meta, _ := object["meta"].(map[string]any)
		record.Truncated = mcpOutputTruncated(meta, result)
	}
}

func mcpOutputTruncated(meta map[string]any, text string) bool {
	if value, ok := meta["truncation"]; ok && jsonFlag(value, "truncated") {
		return true
	}
	var value any
	if json.Unmarshal([]byte(text), &value) == nil {
		return jsonFlag(value, "truncated") || jsonFlag(value, "truncated_global")
	}
	return false
}

func jsonFlag(value any, key string) bool {
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	var object map[string]any
	if json.Unmarshal(data, &object) != nil {
		return false
	}
	flag, _ := object[key].(bool)
	return flag
}

func mcpRequestMetadata(req *server.Request) (method, requestID string) {
	if req == nil {
		return "", ""
	}
	return req.Method(), req.ID().String()
}

func mcpRequestToolName(req *server.Request) string {
	if req == nil || req.Method() != protocol.MethodToolsCall {
		return ""
	}
	var params struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(req.RawParams(), &params) != nil {
		return ""
	}
	if params.Name == mcpRouteTool || params.Name == mcpCallTool {
		return params.Name
	}
	return ""
}
