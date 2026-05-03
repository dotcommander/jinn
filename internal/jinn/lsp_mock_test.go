package jinn

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// mockConfig controls the behavior of runMockServer for variant tests.
// Zero value → normal happy-path responses.
type mockConfig struct {
	slow bool // block forever (timeout test)

	// per-method overrides: when true, the server returns null for that method
	nullDefinition bool
	nullReferences bool
	nullHover      bool
	nullSymbols    bool
	nullRename     bool

	// manyReferences: if > 0, return this many reference locations
	manyReferences int

	// plainHover: return {"contents":"plain text"} instead of markup content
	plainHover bool

	// flatSymbols: return SymbolInformation[] instead of DocumentSymbol[]
	flatSymbols bool

	// badRename: return malformed JSON for rename
	badRename bool
}

// newMockLauncher returns an lspLauncher that runs an in-process mock LSP server
// over io.Pipe pairs. Pass slow=true to make the server block forever (timeout test).
func newMockLauncher(slow bool) lspLauncher {
	return newMockLauncherCfg(mockConfig{slow: slow})
}

// newMockLauncherCfg returns a launcher configured by cfg.
func newMockLauncherCfg(cfg mockConfig) lspLauncher {
	return func(_ []string) (io.WriteCloser, io.ReadCloser, func() error, error) {
		// clientW → serverR: client writes requests, server reads them.
		// serverW → clientR: server writes replies, client reads them.
		serverR, clientW := io.Pipe()
		clientR, serverW := io.Pipe()

		go runMockServer(serverR, serverW, cfg)

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
func runMockServer(r io.Reader, w io.WriteCloser, cfg mockConfig) {
	defer w.Close()

	if cfg.slow {
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
			if cfg.nullDefinition {
				writeMockFrame(w, mockReply(msg.ID, nil))
				continue
			}
			loc := map[string]any{
				"uri": "file:///fake/src.go",
				"range": map[string]any{
					"start": map[string]any{"line": 9, "character": 0},
					"end":   map[string]any{"line": 9, "character": 5},
				},
			}
			writeMockFrame(w, mockReply(msg.ID, []any{loc}))

		case "textDocument/references":
			if cfg.nullReferences {
				writeMockFrame(w, mockReply(msg.ID, nil))
				continue
			}
			count := 3
			if cfg.manyReferences > 0 {
				count = cfg.manyReferences
			}
			locs := make([]any, count)
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
			if cfg.nullHover {
				writeMockFrame(w, mockReply(msg.ID, nil))
				continue
			}
			if cfg.plainHover {
				writeMockFrame(w, mockReply(msg.ID, map[string]any{
					"contents": "plain text",
				}))
				continue
			}
			writeMockFrame(w, mockReply(msg.ID, map[string]any{
				"contents": map[string]any{"kind": "markdown", "value": "func Foo() error"},
			}))

		case "textDocument/documentSymbol":
			if cfg.nullSymbols {
				writeMockFrame(w, mockReply(msg.ID, nil))
				continue
			}
			if cfg.flatSymbols {
				// SymbolInformation[] — flat format with location.uri
				syms := []any{
					map[string]any{
						"name": "foo",
						"kind": 12, // Function
						"location": map[string]any{
							"uri": "file:///a.go",
							"range": map[string]any{
								"start": map[string]any{"line": 0, "character": 0},
								"end":   map[string]any{"line": 0, "character": 3},
							},
						},
					},
				}
				writeMockFrame(w, mockReply(msg.ID, syms))
				continue
			}
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
			if cfg.nullRename {
				writeMockFrame(w, mockReply(msg.ID, nil))
				continue
			}
			if cfg.badRename {
				// Write a well-formed frame containing malformed JSON for the result field.
				// We craft a raw reply where "result" is not a valid lspWorkspaceEdit.
				body := []byte(`{"jsonrpc":"2.0","id":` + fmt.Sprintf("%d", *msg.ID) + `,"result":{"broken":}`)
				fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(body), body) //nolint:errcheck
				continue
			}
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
