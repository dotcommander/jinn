package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/dotcommander/jinn/internal/jinn"
)

var version = "dev"

const helpText = `Usage: jinn [--schema | --inspect [addr] | --mcp | --version | --help]

Sandboxed tool executor for AI coding agents.
Reads a JSON tool request from stdin, writes a JSON response to stdout.

Flags:
  --schema   Print tool definitions (OpenAI function-calling format)
  --inspect  Start a local browser inspector UI (default: 127.0.0.1:8787)
  --mcp      Start stdio MCP discovery broker mode
  --version  Print version
  --help     Print this help

Example:
  echo '{"tool":"read_file","args":{"path":"main.go"}}' | jinn
  jinn --schema | jq .
  jinn --inspect 127.0.0.1:8787
  jinn --mcp
`

// Logging policy: log/slog is intentionally absent from this binary.
//
// jinn speaks a stdin->stdout JSON wire protocol: every response is written
// with json.NewEncoder(os.Stdout).Encode(...) and parsed as JSON by the
// calling agent. A slog handler targeting stdout or stderr would interleave
// non-JSON bytes into that stream and corrupt downstream parsers, so neither
// stream may carry log output. There are currently no diagnostic call sites,
// so a file-sink handler (writing under ~/.config/jinn/, gated by
// JINN_CONFIG_DIR per internal/jinn/config.go) would add a log path, handler
// lifecycle, and an on-disk file with no consumer. If diagnostic logging is
// added later, route it ONLY to such a file sink — never to stdout/stderr.
func main() {
	if err := run(context.Background()); err != nil {
		writeRunError(err)
		os.Exit(1)
	}
}

func writeRunError(err error) {
	resp := jinn.Response{Error: err.Error()}
	var cErr *cliError
	if errors.As(err, &cErr) {
		resp = cErr.resp
	}
	if writeErr := writeResponse(resp); writeErr != nil {
		fmt.Fprintf(os.Stderr, "write response: %s\n", writeErr)
	}
}

type cliError struct {
	resp jinn.Response
}

func (e *cliError) Error() string {
	return e.resp.Error
}

func fail(resp jinn.Response) error {
	return &cliError{resp: resp}
}

func writeResponse(resp jinn.Response) error {
	return json.NewEncoder(os.Stdout).Encode(resp)
}

func run(ctx context.Context) error {
	if len(os.Args) > 1 {
		handled, err := handleFlag(ctx, os.Args[1], os.Args[2:])
		if handled || err != nil {
			return err
		}
	}

	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		fmt.Print(helpText)
		return nil
	}

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	wd, err := os.Getwd()
	if err != nil {
		return fail(jinn.Response{Error: fmt.Sprintf("getwd: %s", err)})
	}

	e := jinn.New(wd, version)
	defer func() { _ = e.Close() }()

	req, err := readRequest()
	if err != nil {
		return err
	}
	attachRequestID(&req)

	result, meta, err := e.Dispatch(sigCtx, req.Tool, req.Args)
	if err != nil {
		return fail(errorResponse(err, meta, req.RequestID))
	}

	applyCompression(req, result)
	return writeResponse(successResponse(req, result, meta))
}

func handleFlag(ctx context.Context, flag string, args []string) (bool, error) {
	switch flag {
	case "--schema":
		schema, err := jinn.LeanSchema()
		if err != nil {
			return true, fail(jinn.Response{Error: fmt.Sprintf("lean schema: %s", err)})
		}
		fmt.Println(schema)
		return true, nil
	case "--version":
		fmt.Println(jinn.ResolveVersion(version))
		return true, nil
	case "--help", "-h":
		fmt.Print(helpText)
		return true, nil
	case "--inspect":
		addr := "127.0.0.1:8787"
		if len(args) > 0 && args[0] != "" {
			addr = args[0]
		}
		return true, serveInspector(ctx, addr, version)
	case "--mcp":
		return true, runMCP(ctx, os.Stdin, os.Stdout, version)
	default:
		return false, nil
	}
}

func readRequest() (jinn.Request, error) {
	var req jinn.Request
	decodeErr := json.NewDecoder(os.Stdin).Decode(&req)
	if decodeErr == nil {
		return req, nil
	}
	if errors.Is(decodeErr, io.EOF) {
		return req, fail(jinn.Response{Error: "no input: pipe a JSON request to stdin (try jinn --help)"})
	}
	return req, fail(jinn.Response{Error: fmt.Sprintf("invalid JSON: %s", decodeErr)})
}

func attachRequestID(req *jinn.Request) {
	if req.RequestID == "" {
		return
	}
	if req.Args == nil {
		req.Args = make(map[string]interface{})
	}
	if _, ok := req.Args["request_id"]; !ok {
		req.Args["request_id"] = req.RequestID
	}
}

func errorResponse(err error, meta map[string]any, requestID string) jinn.Response {
	risk := ""
	classification := ""
	if meta != nil {
		if v, ok := meta["risk"].(string); ok {
			risk = v
		}
		if v, ok := meta["classification"].(string); ok {
			classification = v
		}
	}
	resp := jinn.Response{
		Error:          err.Error(),
		Risk:           risk,
		Classification: classification,
		RequestID:      requestID,
	}
	var sErr *jinn.ErrWithSuggestion
	if errors.As(err, &sErr) {
		resp.Suggestion = sErr.Suggestion
		resp.ErrorCode = sErr.Code
	}
	return resp
}

func applyCompression(req jinn.Request, result *jinn.ToolResult) {
	if !req.Compress || req.Tool == "run_shell" || result.Text == "" {
		return
	}
	var compressMeta jinn.CompressionMeta
	result.Text, compressMeta = jinn.NewCompressor().Compress(result.Text, req.Tool)
	if len(compressMeta.Strategies) == 0 {
		return
	}
	if result.Meta == nil {
		result.Meta = make(map[string]any)
	}
	result.Meta["compression"] = compressMeta
}

func successResponse(req jinn.Request, result *jinn.ToolResult, meta map[string]any) jinn.Response {
	resp := jinn.Response{
		OK:        true,
		Result:    result.Text,
		Content:   result.Content,
		Meta:      result.Meta,
		RequestID: req.RequestID,
	}
	if meta != nil {
		if v, ok := meta["risk"].(string); ok {
			resp.Risk = v
		}
		if v, ok := meta["classification"].(string); ok {
			resp.Classification = v
		}
	}
	return resp
}
