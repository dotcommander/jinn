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
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Method    string `json:"method"`
	RequestID string `json:"requestId"`
	Duration  int64  `json:"durationMs"`
	Outcome   string `json:"outcome"`
	ToolName  string `json:"toolName,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
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
						logger.record(mcpLogRecord{Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Level: string(mcpLogError), Method: method, RequestID: requestID, Outcome: "panic", ErrorCode: "internal_error"})
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
				outcome, code, level := "ok", "", mcpLogInfo
				if err != nil {
					outcome, code, level = "error", "protocol_error", mcpLogError
				} else if call, ok := result.(*protocol.CallToolResult); ok && call.IsError {
					outcome, code, level = "tool_error", "tool_error", mcpLogError
				}
				logger.record(mcpLogRecord{Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Level: string(level), Method: method, RequestID: requestID, Duration: time.Since(started).Milliseconds(), Outcome: outcome, ToolName: mcpRequestToolName(req), ErrorCode: code})
			}
			return result, err
		}
	}
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
