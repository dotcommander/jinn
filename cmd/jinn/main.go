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

const helpText = `Usage: jinn [--schema | --version | --help]

Sandboxed tool executor for AI coding agents.
Reads a JSON tool request from stdin, writes a JSON response to stdout.

Flags:
  --schema   Print tool definitions (OpenAI function-calling format)
  --version  Print version
  --help     Print this help

Example:
  echo '{"tool":"read_file","args":{"path":"main.go"}}' | jinn
  jinn --schema | jq .
`

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--schema":
			schema, err := jinn.LeanSchema()
			if err != nil {
				json.NewEncoder(os.Stdout).Encode(jinn.Response{Error: fmt.Sprintf("lean schema: %s", err)})
				os.Exit(1)
			}
			fmt.Println(schema)
			return
		case "--version":
			fmt.Println(jinn.ResolveVersion(version))
			return
		case "--help", "-h":
			fmt.Print(helpText)
			return
		}
	}

	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		fmt.Print(helpText)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	wd, err := os.Getwd()
	if err != nil {
		json.NewEncoder(os.Stdout).Encode(jinn.Response{Error: fmt.Sprintf("getwd: %s", err)})
		os.Exit(1)
	}

	e := jinn.New(wd, version)

	var req jinn.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			json.NewEncoder(os.Stdout).Encode(jinn.Response{Error: "no input: pipe a JSON request to stdin (try jinn --help)"})
		} else {
			json.NewEncoder(os.Stdout).Encode(jinn.Response{Error: fmt.Sprintf("invalid JSON: %s", err)})
		}
		os.Exit(1)
	}
	if req.Client == "" {
		req.Client = os.Getenv("JINN_CLIENT")
	}
	if req.Client != "" && req.Tool == "related_context" {
		if req.Args == nil {
			req.Args = make(map[string]interface{})
		}
		if _, ok := req.Args["client"]; !ok {
			req.Args["client"] = req.Client
		}
	}

	result, meta, err := e.Dispatch(ctx, req.Tool, req.Args)
	if err != nil {
		risk := ""
		classification := ""
		if meta != nil {
			risk = meta["risk"]
			classification = meta["classification"]
		}
		resp := jinn.Response{
			Error:          err.Error(),
			Risk:           risk,
			Classification: classification,
			RequestID:      req.RequestID,
		}
		// Populate suggestion and error_code fields when the error carries them.
		var sErr *jinn.ErrWithSuggestion
		if errors.As(err, &sErr) {
			resp.Suggestion = sErr.Suggestion
			resp.ErrorCode = sErr.Code
		}
		json.NewEncoder(os.Stdout).Encode(resp)
		os.Exit(1)
	}

	// Apply output compression when requested. run_shell compresses internally
	// (pre-framing); every other tool opts in via req.Compress.
	var compressMeta jinn.CompressionMeta
	if req.Compress && req.Tool != "run_shell" && result.Text != "" {
		result.Text, compressMeta = jinn.NewCompressor().Compress(result.Text, req.Tool)
		// Merge compression metadata into result.Meta.
		if len(compressMeta.Strategies) > 0 {
			if result.Meta == nil {
				result.Meta = make(map[string]any)
			}
			result.Meta["compression"] = compressMeta
		}
	}

	// Build success response. Risk/Classification are only set by run_shell.
	resp := jinn.Response{
		OK:        true,
		Result:    result.Text,
		Content:   result.Content,
		Meta:      result.Meta,
		RequestID: req.RequestID,
	}
	if meta != nil {
		resp.Risk = meta["risk"]
		resp.Classification = meta["classification"]
	}
	json.NewEncoder(os.Stdout).Encode(resp)
}
