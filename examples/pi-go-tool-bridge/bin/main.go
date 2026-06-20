// Command bridge-bin is a minimal example of a Go program that backs a pi
// extension. It reads ONE JSON tool request from stdin and writes ONE JSON
// response to stdout — the same wire protocol the production `jinn` binary uses.
//
//	echo '{"tool":"echo","args":{"text":"hi"}}' | bridge-bin
//	  → {"ok":true,"result":"hi"}
//
// The pi extension (../index.ts) spawns this binary once per tool call. All the
// real logic lives here in Go; the TypeScript side is a thin shim.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// request is the JSON envelope read from stdin.
type request struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

// response is the JSON envelope written to stdout. On success set OK+Result;
// on failure set OK=false+Error. The TS shim maps these to a pi tool result.
type response struct {
	OK     bool   `json:"ok"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func main() {
	// One request per process: read all of stdin, dispatch, print one response.
	in, _ := io.ReadAll(io.LimitReader(os.Stdin, 10<<20)) // 10 MiB cap
	var req request
	if err := json.Unmarshal(in, &req); err != nil {
		writeResponse(response{Error: fmt.Sprintf("invalid JSON request: %s", err)})
		os.Exit(1)
	}

	result, err := dispatch(req)
	if err != nil {
		writeResponse(response{Error: err.Error()})
		os.Exit(1)
	}
	writeResponse(response{OK: true, Result: result})
}

// dispatch routes a tool name to its handler. Add a case per tool — this is the
// one place a Go dev extends the binary's capabilities.
func dispatch(req request) (string, error) {
	switch req.Tool {
	case "echo":
		return toolEcho(req.Args)
	case "read_file":
		return toolReadFile(req.Args)
	default:
		return "", fmt.Errorf("unknown tool: %q", req.Tool)
	}
}

func toolEcho(raw json.RawMessage) (string, error) {
	var a struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("echo: %s", err)
	}
	return a.Text, nil
}

func toolReadFile(raw json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("read_file: %s", err)
	}
	if a.Path == "" {
		return "", fmt.Errorf("read_file: 'path' is required")
	}
	b, err := os.ReadFile(filepath.Clean(a.Path))
	if err != nil {
		return "", fmt.Errorf("read_file: %s", err)
	}
	return string(b), nil
}

func writeResponse(resp response) {
	_ = json.NewEncoder(os.Stdout).Encode(resp)
}
