package jinn

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
)

// lspLauncher launches an LSP server and returns its stdin write end, stdout
// read end, and a kill function. Tests inject a fake launcher over pipes.
type lspLauncher func(argv []string) (stdin io.WriteCloser, stdout io.ReadCloser, kill func() error, err error)

func realLauncher(argv []string) (io.WriteCloser, io.ReadCloser, func() error, error) {
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec
	cmd.Stderr = io.Discard // suppress LSP server stderr noise
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("lsp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("lsp stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("lsp start: %w", err)
	}
	kill := func() error { return cmd.Process.Kill() }
	return stdin, stdout, kill, nil
}

// lspClient drives one LSP session. All calls are synchronous and single-threaded;
// the LSP handshake and query sequence does not require concurrent requests.
type lspClient struct {
	stdin    io.WriteCloser
	stdout   *bufio.Reader
	kill     func() error
	nextID   atomic.Int64
	launcher lspLauncher // nil → use realLauncher
}

// lspRPCMsg is the wire shape for JSON-RPC 2.0 requests and responses.
type lspRPCMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  any             `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *lspRPCError    `json:"error,omitempty"`
}

type lspRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// lspLocation is shared by definition and references responses.
type lspLocation struct {
	URI   string `json:"uri"`
	Range struct {
		Start struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"start"`
	} `json:"range"`
}

func formatLocation(loc lspLocation) string {
	return fmt.Sprintf("%s:%d:%d", loc.URI, loc.Range.Start.Line+1, loc.Range.Start.Character+1)
}

// lspLocationLink is an alternative definition response format.
// Some servers return LocationLink[] instead of Location[].
type lspLocationLink struct {
	TargetURI            string `json:"targetUri"`
	TargetRange          lspRange `json:"targetRange"`
	TargetSelectionRange lspRange `json:"targetSelectionRange"`
}

// lspRange is a reusable LSP range type shared by location links and edits.
type lspRange struct {
	Start struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"start"`
}

// lspWorkspaceEdit is the result of textDocument/rename.
type lspWorkspaceEdit struct {
	Changes map[string][]lspTextEdit `json:"changes,omitempty"`
}

// lspTextEdit is a single text replacement in a document.
type lspTextEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

func newLSPClient(launcher lspLauncher) *lspClient {
	return &lspClient{launcher: launcher}
}

func (c *lspClient) start(argv []string) error {
	launch := c.launcher
	if launch == nil {
		launch = realLauncher
	}
	stdin, stdout, kill, err := launch(argv)
	if err != nil {
		return err
	}
	c.stdin = stdin
	c.stdout = bufio.NewReader(stdout)
	c.kill = kill
	return nil
}

func (c *lspClient) stop() {
	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.kill != nil {
		c.kill() //nolint:errcheck
	}
}

// sendRequest sends a JSON-RPC request and reads the matching reply.
// Relies on the LSP server returning responses in order for the synchronous
// request sequence used here (initialize → didOpen → query → shutdown).
func (c *lspClient) sendRequest(method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	msg := lspRPCMsg{JSONRPC: "2.0", ID: &id, Method: method, Params: params}
	if err := c.writeMsg(msg); err != nil {
		return nil, err
	}
	return c.readReply(id)
}

// sendNotification sends a JSON-RPC notification (no id, no reply expected).
func (c *lspClient) sendNotification(method string, params any) error {
	return c.writeMsg(lspRPCMsg{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *lspClient) writeMsg(msg lspRPCMsg) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("lsp marshal: %w", err)
	}
	_, err = fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

// readReply reads frames until it finds one whose id matches wantID.
// Notifications and out-of-order messages from the server are discarded.
func (c *lspClient) readReply(wantID int64) (json.RawMessage, error) {
	for {
		frame, err := c.readFrame()
		if err != nil {
			return nil, fmt.Errorf("lsp read: %w", err)
		}
		var reply lspRPCMsg
		if err := json.Unmarshal(frame, &reply); err != nil {
			return nil, fmt.Errorf("lsp unmarshal: %w", err)
		}
		if reply.ID == nil || *reply.ID != wantID {
			continue // server notification or different id — skip
		}
		if reply.Error != nil {
			return nil, fmt.Errorf("lsp error %d: %s", reply.Error.Code, reply.Error.Message)
		}
		return reply.Result, nil
	}
}

// readFrame reads one Content-Length framed LSP message from stdout.
func (c *lspClient) readFrame() ([]byte, error) {
	contentLen := -1
	for {
		line, err := c.stdout.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("lsp header read: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of headers
		}
		if strings.HasPrefix(line, "Content-Length:") {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")))
			if err != nil {
				return nil, fmt.Errorf("lsp bad Content-Length: %w", err)
			}
			contentLen = n
		}
	}
	if contentLen < 0 {
		return nil, fmt.Errorf("lsp: missing Content-Length header")
	}
	buf := make([]byte, contentLen)
	if _, err := io.ReadFull(c.stdout, buf); err != nil {
		return nil, fmt.Errorf("lsp body read: %w", err)
	}
	return buf, nil
}

// --- LSP lifecycle ---

func (c *lspClient) handshake(workDir string) error {
	type clientInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	type initParams struct {
		ProcessID    int        `json:"processId"`
		RootURI      string     `json:"rootUri"`
		Capabilities struct{}   `json:"capabilities"`
		ClientInfo   clientInfo `json:"clientInfo"`
	}
	_, err := c.sendRequest("initialize", initParams{
		ProcessID:  os.Getpid(),
		RootURI:    pathToURI(workDir),
		ClientInfo: clientInfo{Name: "jinn", Version: "0.1"},
	})
	if err != nil {
		return fmt.Errorf("lsp initialize: %w", err)
	}
	return c.sendNotification("initialized", struct{}{})
}

// maxLSPFileSize is the upper bound for files sent to didOpen. Files above
// this threshold are too large for useful LSP analysis and risk OOM in the
// language server process.
const maxLSPFileSize = 10 * 1024 * 1024 // 10 MB

func (c *lspClient) didOpen(absPath, langID string) error {
	fi, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("lsp didOpen stat: %w", err)
	}
	if fi.Size() > maxLSPFileSize {
		return &ErrWithSuggestion{
			Err:        fmt.Errorf("lsp didOpen: file too large (%d bytes, max %d)", fi.Size(), maxLSPFileSize),
			Suggestion: "lsp_query works best on files under 10 MB",
			Code:       ErrCodeFileTooLarge,
		}
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("lsp didOpen read: %w", err)
	}
	type textDocItem struct {
		URI        string `json:"uri"`
		LanguageID string `json:"languageId"`
		Version    int    `json:"version"`
		Text       string `json:"text"`
	}
	return c.sendNotification("textDocument/didOpen", map[string]any{
		"textDocument": textDocItem{URI: pathToURI(absPath), LanguageID: langID, Version: 1, Text: string(data)},
	})
}

func (c *lspClient) didClose(absPath string) error {
	return c.sendNotification("textDocument/didClose", map[string]any{
		"textDocument": map[string]string{"uri": pathToURI(absPath)},
	})
}

func (c *lspClient) shutdown() {
	c.sendRequest("shutdown", nil)  //nolint:errcheck
	c.sendNotification("exit", nil) //nolint:errcheck
}

// --- position helpers ---

// lspPosition converts 1-based line/char to 0-based LSP position.
func lspPosition(line, char int) map[string]any {
	return map[string]any{"line": line - 1, "character": char - 1}
}

func tdPos(absPath string, line, char int) map[string]any {
	return map[string]any{
		"textDocument": map[string]string{"uri": pathToURI(absPath)},
		"position":     lspPosition(line, char),
	}
}

// findSymbolColumn reads line (0-based) from absPath and returns the 0-based
// character offset of the first occurrence of symbol. The offset is in runes
// (UTF-16 code units are close enough for BMP; jinn targets ASCII-heavy source).
func findSymbolColumn(absPath string, line int, symbol string) (int, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return 0, fmt.Errorf("find symbol column: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	if line < 0 || line >= len(lines) {
		return 0, fmt.Errorf("line %d out of range (file has %d lines)", line+1, len(lines))
	}
	lineText := lines[line]
	before, _, ok := strings.Cut(lineText, symbol)
	if !ok {
		return 0, fmt.Errorf("symbol %q not found on line %d", symbol, line+1)
	}
	return len([]rune(before)), nil
}

// lspCachedLines reads a file into a line slice, caching results for the
// lifetime of one query. Returns nil on read error (context is best-effort).
func lspCachedLines(cache map[string][]string, path string) []string {
	if lines, ok := cache[path]; ok {
		return lines
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	cache[path] = lines
	return lines
}

// lspFormatContext renders contextSize lines around targetLine (0-based) with
// a "> " marker on the target line. Returns empty string if lines is nil/empty.
func lspFormatContext(lines []string, targetLine, contextSize int) string {
	if len(lines) == 0 || contextSize <= 0 {
		return ""
	}
	start := targetLine - contextSize
	if start < 0 {
		start = 0
	}
	end := targetLine + contextSize + 1
	if end > len(lines) {
		end = len(lines)
	}

	var sb strings.Builder
	for i := start; i < end; i++ {
		marker := "  "
		if i == targetLine {
			marker = "> "
		}
		fmt.Fprintf(&sb, "%s%4d | %s\n", marker, i+1, lines[i])
	}
	return strings.TrimRight(sb.String(), "\n")
}

// formatWorkspaceEdit formats rename results as "file: N edit(s)" + per-edit lines.
// workDir is used to compute relative paths for readability.
func formatWorkspaceEdit(edit *lspWorkspaceEdit, workDir string) string {
	if edit == nil || len(edit.Changes) == 0 {
		return "no changes"
	}
	var sb strings.Builder
	totalEdits := 0
	for uri, edits := range edit.Changes {
		path := strings.TrimPrefix(uri, "file://")
		rel := path
		if workDir != "" {
			if r, err := filepath.Rel(workDir, path); err == nil {
				rel = r
			}
		}
		fmt.Fprintf(&sb, "%s: %d edit(s)\n", rel, len(edits))
		for _, e := range edits {
			line := e.Range.Start.Line + 1
			fmt.Fprintf(&sb, "  line %d: %q\n", line, e.NewText)
		}
		totalEdits += len(edits)
	}
	fmt.Fprintf(&sb, "\n%d file(s), %d edit(s) total", len(edit.Changes), totalEdits)
	return sb.String()
}
