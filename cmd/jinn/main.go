package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/dotcommander/jinn/internal/jinn"
)

var version = "dev"

const helpText = `Usage: jinn [--shell-mode=disabled|sandboxed|unsafe] [--mcp-profile=discover|read-only|network] [mcp ... | web fetch [flags] URL | web search [flags] QUERY | --schema | --inspect [addr] | --mcp [--mcp-profile] | --mcp-http [addr] [--mcp-profile] | --version | --help]

Sandboxed tool executor for AI coding agents.
Reads a JSON tool request from stdin, writes a JSON response to stdout.

Flags:
	--shell-mode  Shell execution policy: disabled, sandboxed, or unsafe (default: disabled)
	--schema      Print tool definitions (OpenAI function-calling format)
	--inspect     Start a local browser inspector UI (default: 127.0.0.1:8787)
	--mcp        Start MCP 2026-07-28 stdio broker (default profile: jinn_route only)
	--mcp-http   Start MCP 2026-07-28 Streamable HTTP at /mcp (default: 127.0.0.1:8788)
	--mcp-profile MCP surface: discover, read-only, or network (default: discover)
	mcp          Explore an MCP endpoint or explicit-argv subprocess; run "jinn mcp --help"
	HTTP auth    Set JINN_MCP_HTTP_TOKEN and JINN_MCP_HTTP_ORIGINS for non-loopback binds
  --version  Print version
	--help     Print this help
	web        Fetch web pages or search the web; run "jinn web --help" for details

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
// JINN_CONFIG_DIR per the convention in internal/jinn/stats.go) would add a log
// path, handler lifecycle, and an on-disk file with no consumer. If diagnostic
// logging is added later, route it ONLY to such a file sink — never to
// stdout/stderr.
func main() {
	if err := run(context.Background()); err != nil {
		writeRunError(err)
		os.Exit(cliExitStatus(err))
	}
}

// cliExitStatus is additive: ordinary CLI failures remain status 1.
func cliExitStatus(err error) int {
	type exitStatus interface{ ExitStatus() int }
	var statusErr exitStatus
	if errors.As(err, &statusErr) && statusErr.ExitStatus() > 0 {
		return statusErr.ExitStatus()
	}
	return 1
}

func writeRunError(err error) {
	type alreadyReported interface{ AlreadyReported() bool }
	var reported alreadyReported
	if errors.As(err, &reported) && reported.AlreadyReported() {
		return
	}
	var doctorErr mcpDoctorReportedError
	if errors.As(err, &doctorErr) {
		return
	}
	var webErr webCLIError
	if errors.As(err, &webErr) {
		if webErr.asJSON {
			if writeErr := renderWebJSONError(webErr.err); writeErr != nil {
				fmt.Fprintf(os.Stderr, "write web JSON error: %s\n", writeErr)
			}
			return
		}
		_, _ = fmt.Fprintln(os.Stderr, webErr.err)
		return
	}
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

//nolint:gocognit,gocyclo,nestif,goconst,revive // This command boundary keeps subcommand and legacy flag routing explicit.
func run(ctx context.Context) error {
	mode, profile, positional, err := parseCLIArgs(os.Args[1:])
	if err != nil {
		return fail(jinn.Response{Error: err.Error(), ErrorCode: jinn.ErrCodeInvalidArgs})
	}
	if len(positional) > 0 {
		if positional[0] == "mcp" {
			if len(positional) == 1 || positional[1] == "--help" || positional[1] == "-h" || positional[1] == "help" {
				_, writeErr := fmt.Fprint(os.Stdout, mcpExplorerHelp)
				return writeErr
			}
			return runMCPExplorer(ctx, positional[1:])
		}
		if positional[0] == "web" {
			if webRunErr := runWeb(ctx, positional[1:]); webRunErr != nil {
				var webErr webCLIError
				if errors.As(webRunErr, &webErr) {
					return webErr
				}
				return webCLIError{err: webRunErr}
			}
			return nil
		}
		handled, flagErr := handleFlagWithProfile(ctx, positional[0], positional[1:], mode, profile)
		if handled || flagErr != nil {
			return flagErr
		}
		return fail(jinn.Response{
			Error:     fmt.Sprintf("unknown command or flag %q", positional[0]),
			ErrorCode: jinn.ErrCodeInvalidArgs,
		})
	}

	if fi, statErr := os.Stdin.Stat(); statErr == nil && fi.Mode()&os.ModeCharDevice != 0 {
		fmt.Print(helpText)
		return nil
	}

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	input := io.Reader(os.Stdin)
	if len(positional) == 0 {
		var isMCP bool
		input, isMCP = detectPipedMCP(input)
		if isMCP {
			return runMCPWithProfile(sigCtx, input, os.Stdout, version, mode, profile)
		}
	}

	wd, err := os.Getwd()
	if err != nil {
		return fail(jinn.Response{Error: fmt.Sprintf("getwd: %s", err)})
	}

	e, err := jinn.NewWithConfig(wd, jinn.EngineConfig{Version: version, ShellMode: mode, Web: webConfig()})
	if err != nil {
		return fail(jinn.Response{Error: err.Error(), ErrorCode: jinn.ErrCodeInvalidArgs})
	}
	defer func() { _ = e.Close() }()

	req, err := readRequest(input)
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

//nolint:goconst // Repeated CLI flag spellings are the public command grammar, not shared data.
func handleFlagWithProfile(ctx context.Context, flag string, args []string, mode jinn.ShellMode, profile mcpProfile) (bool, error) {
	switch flag {
	case "--schema":
		schema, err := jinn.LeanSchemaForMode(mode)
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
		return true, serveInspector(ctx, addr, version, mode)
	case "--mcp":
		return true, runMCPWithProfile(ctx, os.Stdin, os.Stdout, version, mode, profile)
	case "--mcp-http":
		addr := mcpHTTPDefaultAddr
		if len(args) > 0 && args[0] != "" {
			addr = args[0]
		}
		return true, serveMCPHTTP(ctx, addr, version, mode, profile)
	default:
		return false, nil
	}
}

//nolint:gocognit,gocyclo,gosec,goconst,revive // The explicit bounds checks and flag spellings keep compatibility behavior inspectable.
func parseCLIArgs(arguments []string) (jinn.ShellMode, mcpProfile, []string, error) {
	mode := jinn.ShellModeDisabled
	profile := mcpProfileDiscover
	profileSet := false
	positional := make([]string, 0, len(arguments))
	for i := 0; i < len(arguments); i++ {
		arg := arguments[i]
		if len(positional) == 0 && (arg == "mcp" || arg == "web") {
			positional = append(positional, arguments[i:]...)
			break
		}
		var value string
		switch {
		case strings.HasPrefix(arg, "--shell-mode="):
			value = strings.TrimPrefix(arg, "--shell-mode=")
		case arg == "--shell-mode":
			if i+1 >= len(arguments) {
				return "", "", nil, errors.New("--shell-mode requires a value")
			}
			i++
			value = arguments[i]
		case strings.HasPrefix(arg, "--mcp-profile="):
			profileValue := strings.TrimPrefix(arg, "--mcp-profile=")
			parsed, err := parseMCPProfile(profileValue)
			if err != nil {
				return "", "", nil, err
			}
			profile = parsed
			profileSet = true
			continue
		case arg == "--mcp-profile":
			if i+1 >= len(arguments) {
				return "", "", nil, errors.New("--mcp-profile requires a value")
			}
			i++
			parsed, err := parseMCPProfile(arguments[i])
			if err != nil {
				return "", "", nil, err
			}
			profile = parsed
			profileSet = true
			continue
		default:
			positional = append(positional, arg)
			continue
		}
		parsed, err := jinn.ParseShellMode(value)
		if err != nil {
			return "", "", nil, err
		}
		mode = parsed
	}
	if profileSet && (len(positional) == 0 || (positional[0] != "--mcp" && positional[0] != "--mcp-http")) {
		return "", "", nil, errors.New("--mcp-profile requires --mcp or --mcp-http")
	}
	return mode, profile, positional, nil
}

func readRequest(reader io.Reader) (jinn.Request, error) {
	req, decodeErr := jinn.DecodeOneRequest(reader, 16<<20)
	if decodeErr == nil {
		return req, nil
	}
	if errors.Is(decodeErr, io.EOF) {
		return req, fail(jinn.Response{Error: "no input: pipe a JSON request to stdin (try jinn --help)"})
	}
	return req, fail(jinn.Response{Error: fmt.Sprintf("invalid JSON: %s", decodeErr)})
}

func attachRequestID(req *jinn.Request) {
	if req.RequestID == "" || req.Tool != "memory" {
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

//nolint:goconst // Tool names are stable wire identifiers and remain explicit at this boundary.
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
