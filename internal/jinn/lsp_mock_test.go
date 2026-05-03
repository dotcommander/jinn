package jinn

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// newMockLauncher returns an lspLauncher that runs an in-process mock LSP server
// over io.Pipe pairs. Pass slow=true to make the server block forever (timeout test).
func newMockLauncher(slow bool) lspLauncher {
	return func(_ []string) (io.WriteCloser, io.ReadCloser, func() error, error) {
		// clientW → serverR: client writes requests, server reads them.
		// serverW → clientR: server writes replies, client reads them.
		serverR, clientW := io.Pipe()
		clientR, serverW := io.Pipe()

		go runMockServer(serverR, serverW, slow)

		kill := func() error {
			clientW.Close()
			serverW.Close()
			return nil
		}
		return clientW, clientR, kill, nil
	}
}

// runMockServer handles JSON-RPC 2.0 frames over r/w. It responds to the
// exact methods exercised by the test suite; unrecognised methods are ignored.
func runMockServer(r io.Reader, w io.WriteCloser, slow bool) {
	defer w.Close()

	if slow {
		// Block until the pipe is closed by the client's kill func.
		buf := make([]byte, 1)
		r.Read(buf) //nolint:errcheck
		return
	}

	// Borrow lspClient.readFrame by constructing a minimal client pointed at r.
	reader := &lspClient{stdout: bufio.NewReader(r), stdin: nopWriteCloser{w}}

	for {
		frame, err := reader.readFrame()
		if err != nil {
			return
		}
		var msg lspRPCMsg
		if err := json.Unmarshal(frame, &msg); err != nil {
			return
		}

		switch msg.Method {
		case "initialize":
			writeMockFrame(w, mockReply(msg.ID, map[string]any{"capabilities": map[string]any{}}))

		case "initialized", "textDocument/didOpen", "textDocument/didClose", "exit":
			// notifications — no reply required

		case "textDocument/definition":
			loc := map[string]any{
				"uri": "file:///fake/src.go",
				"range": map[string]any{
					"start": map[string]any{"line": 9, "character": 0},
					"end":   map[string]any{"line": 9, "character": 5},
				},
			}
			writeMockFrame(w, mockReply(msg.ID, []any{loc}))

		case "textDocument/references":
			locs := make([]any, 3)
			for i := range locs {
				locs[i] = map[string]any{
					"uri": fmt.Sprintf("file:///fake/ref%d.go", i),
					"range": map[string]any{
						"start": map[string]any{"line": i * 10, "character": i},
						"end":   map[string]any{"line": i * 10, "character": i + 3},
					},
				}
			}
			writeMockFrame(w, mockReply(msg.ID, locs))

		case "textDocument/hover":
			writeMockFrame(w, mockReply(msg.ID, map[string]any{
				"contents": map[string]any{"kind": "markdown", "value": "func Foo() error"},
			}))

		case "textDocument/documentSymbol":
			syms := []any{
				map[string]any{
					"name": "Foo",
					"kind": 12, // Function
					"range": map[string]any{
						"start": map[string]any{"line": 4, "character": 0},
						"end":   map[string]any{"line": 4, "character": 3},
					},
					"selectionRange": map[string]any{
						"start": map[string]any{"line": 4, "character": 0},
						"end":   map[string]any{"line": 4, "character": 3},
					},
				},
				map[string]any{
					"name": "Bar",
					"kind": 13, // Variable
					"range": map[string]any{
						"start": map[string]any{"line": 9, "character": 0},
						"end":   map[string]any{"line": 9, "character": 3},
					},
					"selectionRange": map[string]any{
						"start": map[string]any{"line": 9, "character": 0},
						"end":   map[string]any{"line": 9, "character": 3},
					},
				},
			}
			writeMockFrame(w, mockReply(msg.ID, syms))

		case "textDocument/rename":
			// Return a workspace edit renaming the symbol in one file.
			edit := map[string]any{
				"changes": map[string]any{
					"file:///fake/src.go": []any{
						map[string]any{
							"range": map[string]any{
								"start": map[string]any{"line": 3, "character": 5},
								"end":   map[string]any{"line": 3, "character": 8},
							},
							"newText": "newName",
						},
					},
				},
			}
			writeMockFrame(w, mockReply(msg.ID, edit))

		case "shutdown":
			writeMockFrame(w, mockReply(msg.ID, nil))
		}
	}
}

func mockReply(id *int64, result any) lspRPCMsg {
	raw, _ := json.Marshal(result)
	return lspRPCMsg{JSONRPC: "2.0", ID: id, Result: json.RawMessage(raw)}
}

func writeMockFrame(w io.Writer, msg lspRPCMsg) {
	body, _ := json.Marshal(msg)
	fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(body), body) //nolint:errcheck
}

// fakeLauncherError returns a launcher that immediately fails with err.
// Used to simulate a missing server binary.
func fakeLauncherError(err error) lspLauncher {
	return func(_ []string) (io.WriteCloser, io.ReadCloser, func() error, error) {
		return nil, nil, nil, err
	}
}

// nopWriteCloser wraps an io.Writer with a no-op Close to satisfy io.WriteCloser.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
