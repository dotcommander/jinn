package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dotcommander/jinn/internal/jinn"
	"github.com/voocel/mcp-sdk-go/protocol"
)

func TestMCPAuditC101InitializeNegotiation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, params, version string }{
		{"bare", `{}`, protocol.Version},
		{"current", `{"protocolVersion":"2026-07-28"}`, protocol.Version},
		{"legacy", `{"protocolVersion":"2025-06-18"}`, mcpProtocolVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inR, inW := io.Pipe()
			t.Cleanup(func() { _ = inR.Close(); _ = inW.Close() })
			var out bytes.Buffer
			done := make(chan error, 1)
			go func() {
				done <- runMCPWithProfile(t.Context(), inR, &out, "test", jinn.ShellModeDisabled, mcpProfileDiscover)
			}()
			_, err := io.WriteString(inW, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":`+tc.params+`}`+"\n")
			if err != nil {
				t.Fatal(err)
			}
			_ = inW.Close()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			var reply struct {
				Result struct {
					ProtocolVersion string `json:"protocolVersion"`
				} `json:"result"`
			}
			if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
				t.Fatalf("reply %q: %v", out.String(), err)
			}
			if reply.Result.ProtocolVersion != tc.version {
				t.Fatalf("version = %q, want %q; reply %s", reply.Result.ProtocolVersion, tc.version, out.String())
			}
		})
	}
}

func TestLegacyAuditC102InterleavedPing(t *testing.T) {
	t.Parallel()
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"elicitation":{"form":{}}}}}`,
		`{"jsonrpc":"2.0","id":"call","method":"tools/call","params":{"name":"ask_user_question","arguments":{"questions":[{"question":"Continue?","header":"Choice","options":[{"value":"yes","label":"Yes","description":"Continue"},{"value":"no","label":"No","description":"Stop"}]}]}}}`,
		`{"jsonrpc":"2.0","id":"ping","method":"ping"}`,
		`{"jsonrpc":"2.0","id":"jinn-elicitation-1","result":{"action":"cancel"}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := runLegacyMCP(t.Context(), strings.NewReader(input), &out, "test", jinn.ShellModeDisabled); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n"))
	if len(lines) != 4 {
		t.Fatalf("responses = %d: %s", len(lines), out.String())
	}
	for i, want := range []string{"1", "jinn-elicitation-1", "call", "ping"} {
		var response struct {
			ID     any `json:"id"`
			Result any `json:"result"`
		}
		if err := json.Unmarshal(lines[i], &response); err != nil {
			t.Fatal(err)
		}
		if (i == 0 && response.ID != float64(1)) || (i != 0 && response.ID != want) {
			t.Fatalf("response %d id = %v, want %q", i, response.ID, want)
		}
		if i >= 2 && response.Result == nil {
			t.Fatalf("missing result: %s", lines[i])
		}
	}
}

func TestMCPHTTPAuditC103UncooperativeHandlerRetainsEngine(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "probe.txt"), []byte("available"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := jinn.New(root, "test")
	t.Cleanup(func() { _ = engine.Close() })
	entered, release, drained := make(chan struct{}), make(chan struct{}), make(chan struct{})
	handlerResult := make(chan error, 1)
	srv := newMCPHTTPServer("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _, err := engine.Dispatch(r.Context(), "read_file", map[string]any{"path": "probe.txt"})
		handlerResult <- err
		w.WriteHeader(http.StatusNoContent)
	}))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- serveMCPHTTPServerWithGrace(ctx, srv, listener, 0, func() { _ = engine.Close(); close(drained) })
	}()
	requestDone := make(chan struct{})
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			_ = resp.Body.Close()
		}
		close(requestDone)
	}()
	<-entered
	cancel()
	bound, stop := context.WithTimeout(t.Context(), 2*time.Second)
	t.Cleanup(stop)
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown error = %v", err)
		}
	case <-bound.Done():
		close(release)
		t.Fatal("shutdown did not return promptly")
	}
	select {
	case <-drained:
		close(release)
		t.Fatal("resource closed before handler exit")
	default:
	}
	close(release)
	if err := <-handlerResult; err != nil {
		t.Fatalf("handler used prematurely closed engine: %v", err)
	}
	<-drained
	<-requestDone
}

func TestMCPExplorerAuditC104InvalidPointerBeforeCall(t *testing.T) {
	t.Parallel()
	for _, pointer := range []string{"invalid", "/a~2b"} {
		t.Run(pointer, func(t *testing.T) {
			t.Parallel()
			calls := 0
			config, err := parseMCPExplorer([]string{"call", "https://example.test/mcp", "tool", "--select", pointer})
			config.action = mcpExplorerActionCall
			config.projection = mcpExplorerProjectionOptions{pointer: pointer, hasSelect: true}
			_, callErr := callMCPExplorerTool(t.Context(), config, func(context.Context, string, map[string]any) (*protocol.CallToolResult, error) {
				calls++
				return &protocol.CallToolResult{}, nil
			})
			var projection *mcpExplorerProjectionError
			if !errors.As(err, &projection) || !errors.As(callErr, &projection) || calls != 0 {
				t.Fatalf("parse error/calls = %v/%d", err, calls)
			}
		})
	}
	config, err := parseMCPExplorer([]string{"call", "https://example.test/mcp", "tool", "--select", "/structuredContent/items/1/name"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err = callMCPExplorerTool(t.Context(), config, func(context.Context, string, map[string]any) (*protocol.CallToolResult, error) {
		calls++
		return &protocol.CallToolResult{}, nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("valid pointer calls/error = %d/%v", calls, err)
	}
	var out bytes.Buffer
	if err := writeMCPExplorerCallOutput(&out, mcpExplorerProjectionTestOutput(), config.projection); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"value":"second"`) {
		t.Fatalf("projection: %s", out.String())
	}
}

func TestMCPExplorerAuditC105ErrorMaxBytes(t *testing.T) {
	t.Parallel()
	output := mcpExplorerCallOutput{ResultType: protocol.ResultTypeComplete, IsError: true, Content: protocol.ContentList{protocol.NewTextContent("E_TOOL: " + strings.Repeat("long error ", 1000))}}
	for _, format := range []string{mcpExplorerFormatJSON, mcpExplorerFormatHuman} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			var buffer bytes.Buffer
			err := writeMCPExplorerCallFormattedOutput(&buffer, format, output, mcpExplorerProjectionOptions{maxBytes: 200, hasMaxBytes: true})
			if err != nil {
				t.Fatal(err)
			}
			if buffer.Len() > 200 || !strings.Contains(buffer.String(), "E_TOOL") || !strings.Contains(buffer.String(), "truncated") || !strings.Contains(buffer.String(), `"isError": true`) && !strings.Contains(buffer.String(), `"isError":true`) {
				t.Fatalf("error output %d bytes: %s", buffer.Len(), buffer.String())
			}
			var decoded mcpExplorerCallOutput
			if err := json.Unmarshal(buffer.Bytes(), &decoded); err != nil || !decoded.IsError {
				t.Fatalf("invalid error JSON: %v, %s", err, buffer.String())
			}
		})
	}
}
