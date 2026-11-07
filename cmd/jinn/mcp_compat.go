package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

func currentMCPInitializePrefix(prefix []byte) (line, rest []byte, ok bool) {
	// readMCPPrefix may have consumed leading blank lines.
	for start := 0; start < len(prefix); {
		end := bytes.IndexByte(prefix[start:], '\n')
		if end < 0 {
			end = len(prefix) - start
		}
		line := prefix[start : start+end]
		if len(bytes.TrimSpace(line)) != 0 {
			var probe struct {
				Method string `json:"method"`
			}
			if json.Unmarshal(line, &probe) == nil && probe.Method == mcpMethodInitialize {
				return line, prefix[start+end:], true
			}
			break
		}
		start += end + 1
	}
	return nil, nil, false
}

func allowsLegacyMCP(profile mcpProfile) bool {
	return profile == mcpProfileDiscover
}

func readMCPPrefix(reader *bufio.Reader) ([]byte, bool, error) {
	var prefix bytes.Buffer
	for {
		line, err := readMCPProbeLine(reader)
		if line != "" {
			_, _ = prefix.WriteString(line)
		}
		if strings.TrimSpace(line) != "" {
			return prefix.Bytes(), isLegacyMCPLine([]byte(line)), nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return prefix.Bytes(), false, nil
			}
			return prefix.Bytes(), false, err
		}
	}
}

// detectPipedMCP lets executable-only hosts start Jinn without command-line
// arguments while preserving the existing one-shot JSON protocol. Bytes read
// during protocol selection are replayed unchanged to the selected decoder.
//
//nolint:goconst // JSON-RPC's fixed wire version is intentionally repeated across compatibility paths.
func detectPipedMCP(in io.Reader) (io.Reader, bool) {
	var captured bytes.Buffer
	limited := &io.LimitedReader{R: in, N: mcpProbeMaxLineBytes + 1}
	decoder := json.NewDecoder(io.TeeReader(limited, &captured))
	var envelope struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
	}
	decodeErr := decoder.Decode(&envelope)
	replay := io.MultiReader(bytes.NewReader(captured.Bytes()), in)
	if decodeErr != nil {
		return replay, false
	}
	return replay, envelope.JSONRPC == "2.0" && envelope.Method != ""
}

func readMCPProbeLine(reader *bufio.Reader) (string, error) {
	var line []byte
	for {
		fragment, isPrefix, err := reader.ReadLine()
		line = append(line, fragment...)
		if len(line) > mcpProbeMaxLineBytes {
			return "", fmt.Errorf("MCP request exceeds %d bytes", mcpProbeMaxLineBytes)
		}
		if err != nil {
			return string(line), err
		}
		if !isPrefix {
			line = append(line, '\n')
			return string(line), nil
		}
	}
}

//nolint:goconst // Method and null literals are the legacy JSON-RPC wire grammar.
func isLegacyMCPLine(line []byte) bool {
	var probe struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return false
	}
	if probe.Method == "initialize" {
		var initialize struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		return json.Unmarshal(probe.Params, &initialize) == nil && initialize.ProtocolVersion == mcpProtocolVersion
	}
	switch probe.Method {
	case "ping", "tools/list", "tools/call", "resources/list":
		var params map[string]any
		if len(probe.Params) == 0 || string(probe.Params) == "null" {
			return true
		}
		if err := json.Unmarshal(probe.Params, &params); err != nil {
			return true
		}
		_, hasMeta := params["_meta"]
		return !hasMeta
	default:
		return false
	}
}
