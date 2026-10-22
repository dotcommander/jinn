package jinn

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func symbolReplyClient(t *testing.T, symbols any) *lspClient {
	t.Helper()
	id := int64(1)
	body, err := json.Marshal(mockReply(&id, symbols))
	if err != nil {
		t.Fatal(err)
	}
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	return &lspClient{
		stdin:  nopWriteCloser{io.Discard},
		stdout: bufio.NewReader(bytes.NewBufferString(frame)),
	}
}

func TestDocumentSymbolsCoordinates(t *testing.T) {
	t.Parallel()
	position := map[string]int{"line": 6, "character": 3}
	rangeValue := map[string]any{"start": position, "end": position}
	for _, shape := range []string{"flat", "hierarchical"} {
		t.Run(shape, func(t *testing.T) {
			t.Parallel()
			symbol := map[string]any{"name": "Greet", "kind": 12}
			if shape == "flat" {
				symbol["location"] = map[string]any{"uri": "file:///main.go", "range": rangeValue}
			} else {
				symbol["range"] = rangeValue
				symbol["selectionRange"] = rangeValue
			}
			symbols := []any{symbol}
			out, err := symbolReplyClient(t, symbols).symbols("main.go")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "Greet (line 7)") {
				t.Fatalf("symbol coordinates lost: %s", out)
			}
			line, character, flat, err := symbolReplyClient(t, symbols).resolveSymbolPosition("main.go", "Greet")
			if err != nil {
				t.Fatal(err)
			}
			if line != 7 || character != 4 || flat != (shape == "flat") {
				t.Fatalf("resolved %d:%d flat=%v, want 7:4 flat=%v", line, character, flat, shape == "flat")
			}
			e, dir := testEngine(t)
			path := filepath.Join(dir, "main.go")
			if err := os.WriteFile(path, []byte(strings.Repeat("\n", 6)+"func Greet() {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			req, err := e.resolveSymbolRequest(symbolReplyClient(t, symbols), lspRequest{
				action: "hover", absPath: path, symbol: "Greet",
			})
			if err != nil {
				t.Fatal(err)
			}
			wantColumn := 4 // Hierarchical selection coordinates must remain exact.
			if shape == "flat" {
				wantColumn = 6 // Flat ranges must resolve the identifier, not "func".
			}
			if req.line != 7 || req.char != wantColumn {
				t.Fatalf("request resolved %d:%d, want 7:%d", req.line, req.char, wantColumn)
			}
		})
	}
}
