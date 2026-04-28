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
			fmt.Println(jinn.Schema)
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

	e := jinn.New(wd)

	var req jinn.Request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			json.NewEncoder(os.Stdout).Encode(jinn.Response{Error: "no input: pipe a JSON request to stdin (try jinn --help)"})
		} else {
			json.NewEncoder(os.Stdout).Encode(jinn.Response{Error: fmt.Sprintf("invalid JSON: %s", err)})
		}
		os.Exit(1)
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
		}
		// Populate suggestion field when the error carries one.
		var sErr *jinn.ErrWithSuggestion
		if errors.As(err, &sErr) {
			resp.Suggestion = sErr.Suggestion
		}
		json.NewEncoder(os.Stdout).Encode(resp)
		os.Exit(1)
	}

	// Build success response. Risk/Classification are only set by run_shell.
	resp := jinn.Response{
		OK:      true,
		Result:  result.Text,
		Content: result.Content,
		Meta:    result.Meta,
	}
	if meta != nil {
		resp.Risk = meta["risk"]
		resp.Classification = meta["classification"]
	}
	json.NewEncoder(os.Stdout).Encode(resp)
}
