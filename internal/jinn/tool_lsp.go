package jinn

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// lspExtEntry holds everything jinn needs to know about an LSP-supported extension.
// Adding a new language requires one entry here — argv, langID, and install hint
// are kept together so they cannot drift apart.
type lspExtEntry struct {
	argv    []string // LSP server argv (binary + flags)
	langID  string   // LSP languageId sent in textDocument/didOpen
	install string   // install hint surfaced when the binary is missing
}

// lspExtTable is the single source of truth for all supported extensions.
// Read-only after init — Go has no const for maps.
var lspExtTable = map[string]lspExtEntry{ //nolint:gochecknoglobals
	".go":   {[]string{"gopls", "serve"}, "go", "go install golang.org/x/tools/gopls@latest"},
	".rs":   {[]string{"rust-analyzer"}, "rust", "rustup component add rust-analyzer"},
	".py":   {[]string{"pylsp"}, "python", "pip install python-lsp-server"},
	".ts":   {[]string{"typescript-language-server", "--stdio"}, "typescript", "npm install -g typescript-language-server typescript"},
	".tsx":  {[]string{"typescript-language-server", "--stdio"}, "typescriptreact", "npm install -g typescript-language-server typescript"},
	".js":   {[]string{"typescript-language-server", "--stdio"}, "javascript", "npm install -g typescript-language-server typescript"},
	".jsx":  {[]string{"typescript-language-server", "--stdio"}, "javascriptreact", "npm install -g typescript-language-server typescript"},
	".c":    {[]string{"clangd"}, "c", "install clangd (bundled with LLVM or via system package manager)"},
	".h":    {[]string{"clangd"}, "c", "install clangd (bundled with LLVM or via system package manager)"},
	".cpp":  {[]string{"clangd"}, "cpp", "install clangd (bundled with LLVM or via system package manager)"},
	".cc":   {[]string{"clangd"}, "cpp", "install clangd (bundled with LLVM or via system package manager)"},
	".cxx":  {[]string{"clangd"}, "cpp", "install clangd (bundled with LLVM or via system package manager)"},
	".hpp":  {[]string{"clangd"}, "cpp", "install clangd (bundled with LLVM or via system package manager)"},
	".java": {[]string{"jdtls"}, "java", "install jdtls (Eclipse JDT Language Server)"},
	".lua":  {[]string{"lua-language-server"}, "lua", "install lua-language-server (https://github.com/LuaLS/lua-language-server)"},
	".zig":  {[]string{"zls"}, "zig", "install zls (https://github.com/zigtools/zls)"},
}

// supportedLSPExts returns the sorted, comma-separated list of extensions
// known to lspExtTable — the single source of truth — so the suggestion
// message cannot drift from the actual table.
func supportedLSPExts() string {
	exts := make([]string, 0, len(lspExtTable))
	for ext := range lspExtTable {
		exts = append(exts, ext)
	}
	sort.Strings(exts)
	return strings.Join(exts, ", ")
}

// lspServerForExt returns the LSP server argv for ext.
// Returns ErrWithSuggestion when the extension is unknown or the binary is absent.
func lspServerForExt(ext string) ([]string, error) {
	e, ok := lspExtTable[ext]
	if !ok {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("no LSP server known for extension %s", ext),
			Suggestion: "supported extensions: " + supportedLSPExts(),
			Code:       ErrCodeLspUnavailable,
		}
	}
	if _, err := exec.LookPath(e.argv[0]); err != nil {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("LSP server %q not found on PATH", e.argv[0]),
			Suggestion: fmt.Sprintf("install with: %s", e.install),
			Code:       ErrCodeLspUnavailable,
		}
	}
	return e.argv, nil
}

// pathToURI converts an absolute path to a file:// URI.
// Note: Windows drive-letter paths (C:\...) are not handled; jinn targets unix-first.
func pathToURI(abs string) string {
	return "file://" + abs
}

// langIDForExt returns the LSP language identifier for a file extension.
func langIDForExt(ext string) string {
	if e, ok := lspExtTable[ext]; ok {
		return e.langID
	}
	return "text"
}

func (e *Engine) lspQuery(ctx context.Context, args map[string]interface{}) (string, error) {
	return e.lspQueryWithLauncher(ctx, args, nil)
}

// lspRequest is the validated, resolved form of an lsp_query invocation,
// produced by parseLSPArgs and consumed by the client-dispatch phase.
type lspRequest struct {
	action  string
	absPath string
	ext     string
	argv    []string
	line    int
	char    int
	symbol  string
	newName string
}

// parseLSPArgs validates the raw args, resolves the path, picks the LSP server,
// and auto-detects the column from a symbol when needed.
func (e *Engine) parseLSPArgs(args map[string]interface{}) (lspRequest, error) {
	action, _ := args["action"].(string)
	path, _ := args["path"].(string)
	line := intArg(args, "line", 0)
	char := intArg(args, "character", 0)
	symbol, _ := args["symbol"].(string)
	newName, _ := args["new_name"].(string)

	if action == "" {
		return lspRequest{}, &ErrWithSuggestion{
			Err:        errors.New("lsp_query: 'action' is required"),
			Suggestion: "set action to one of: definition, references, hover, symbols, diagnostics, rename",
			Code:       ErrCodeInvalidArgs,
		}
	}
	if path == "" {
		return lspRequest{}, &ErrWithSuggestion{
			Err:        errors.New("lsp_query: 'path' is required"),
			Suggestion: "provide the path to the source file to query",
			Code:       ErrCodeInvalidArgs,
		}
	}
	absPath, err := e.checkPath(path)
	if err != nil {
		return lspRequest{}, err
	}

	ext := strings.ToLower(filepath.Ext(absPath))
	argv, err := lspServerForExt(ext)
	if err != nil {
		if !(action == "symbols" && ext == ".go") {
			return lspRequest{}, err
		}
		argv = nil
	}

	// rename requires new_name
	if action == "rename" && newName == "" {
		return lspRequest{}, &ErrWithSuggestion{
			Err:        fmt.Errorf("lsp_query: 'new_name' is required for action %q", action),
			Suggestion: "provide the new name for the symbol",
			Code:       ErrCodeInvalidArgs,
		}
	}

	char, err = resolveLSPPosition(action, absPath, line, char, symbol)
	if err != nil {
		return lspRequest{}, err
	}

	return lspRequest{
		action:  action,
		absPath: absPath,
		ext:     ext,
		argv:    argv,
		line:    line,
		char:    char,
		symbol:  symbol,
		newName: newName,
	}, nil
}

// resolveLSPPosition validates that a 1-based line/character position is
// present for actions that require one, auto-detecting the column from a symbol
// when character is unset. Whole-file actions (symbols, diagnostics) need no
// position and pass char through unchanged.
func resolveLSPPosition(action, absPath string, line, char int, symbol string) (int, error) {
	needsPosition := action != "symbols" && action != "diagnostics"
	if !needsPosition {
		return char, nil
	}

	if line <= 0 {
		// A symbol without a line is resolved later from the file's document
		// symbols (that needs an open document, so it happens in the session).
		if symbol != "" {
			return 0, nil
		}
		return 0, &ErrWithSuggestion{
			Err:        fmt.Errorf("lsp_query: 'line' is required for action %q", action),
			Suggestion: "provide a 1-based line number, or set 'symbol' to resolve the position automatically",
			Code:       ErrCodeInvalidArgs,
		}
	}

	// symbol → character auto-detect: read the file line, find the symbol
	if char <= 0 && symbol != "" {
		col, err := findSymbolColumn(absPath, line-1, symbol) // line is 1-based, findSymbolColumn wants 0-based
		if err != nil {
			return 0, &ErrWithSuggestion{
				Err:        fmt.Errorf("lsp_query: %w", err),
				Suggestion: "check that the symbol name appears on the specified line",
				Code:       ErrCodeInvalidArgs,
			}
		}
		char = col + 1 // convert 0-based back to 1-based for the rest of the flow
	}

	if char <= 0 {
		return 0, &ErrWithSuggestion{
			Err:        fmt.Errorf("lsp_query: 'character' (or 'symbol') is required for action %q", action),
			Suggestion: "provide 1-based character offset, or set 'symbol' to auto-detect the column",
			Code:       ErrCodeInvalidArgs,
		}
	}

	return char, nil
}

// dispatchLSPAction runs the query for req.action against an already-open client.
func (e *Engine) dispatchLSPAction(client *lspClient, req lspRequest) (string, error) {
	switch req.action {
	case "definition":
		return client.definition(req.absPath, req.line, req.char, e.workDir, e.checkPath)
	case "references":
		return client.references(req.absPath, req.line, req.char, e.workDir, e.checkPath)
	case "hover":
		return client.hover(req.absPath, req.line, req.char)
	case "symbols":
		return client.symbols(req.absPath)
	case "diagnostics":
		return client.diagnostics(req.absPath)
	case "rename":
		return client.rename(req.absPath, req.line, req.char, req.newName, e.workDir)
	default:
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("unknown lsp action: %s", req.action),
			Suggestion: "use one of: definition, references, hover, symbols, diagnostics, rename",
			Code:       ErrCodeInvalidArgs,
		}
	}
}

// runLSPSession drives the in-session steps for an already-started client:
// handshake → didOpen → dispatch. It does NOT close/shutdown/stop the client —
// that cleanup is owned by the caller's goroutine defer so it runs after the
// result is delivered. Errors short-circuit and propagate to the caller.
func (e *Engine) runLSPSession(client *lspClient, req lspRequest) (string, error) {
	if err := client.handshake(e.workDir); err != nil {
		return "", err
	}
	if err := client.didOpen(req.absPath, langIDForExt(req.ext)); err != nil {
		return "", err
	}
	// Symbol-only position actions: resolve line+char from document symbols now
	// that the document is open, then dispatch at the resolved position.
	if req.needsSymbolResolution() {
		line, char, err := client.resolveSymbolPosition(req.absPath, req.symbol)
		if err != nil {
			return "", err
		}
		req.line, req.char = line, char
	}
	return e.dispatchLSPAction(client, req)
}

// needsSymbolResolution reports whether the request is a position action that
// arrived with a symbol but no line, so its position must be resolved from the
// file's document symbols before dispatch.
func (r lspRequest) needsSymbolResolution() bool {
	if r.action == "symbols" || r.action == "diagnostics" {
		return false
	}
	return r.line <= 0 && r.symbol != ""
}

// lspQueryWithLauncher is the testable variant — tests inject a fake launcher.
func (e *Engine) lspQueryWithLauncher(ctx context.Context, args map[string]interface{}, launcher lspLauncher) (string, error) {
	req, err := e.parseLSPArgs(args)
	if err != nil {
		return "", err
	}

	timeout := e.LSPTimeoutSec
	if timeout <= 0 {
		timeout = 10
	}
	if launcher == nil && req.action == "diagnostics" && req.ext == ".go" {
		return e.goDiagnostics(ctx, req, timeout)
	}
	if launcher == nil && req.action == "symbols" && req.ext == ".go" && len(req.argv) == 0 {
		return goASTSymbols(req.absPath)
	}

	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)

	client := newLSPClient(launcher)
	if err := client.start(ctx, req.argv); err != nil {
		return "", err
	}

	go func() {
		// Cleanup stays in the closure so it runs AFTER the channel send
		// (defer fires at closure exit, post-send) — preserving the original
		// ordering relative to the select/timeout consumer.
		defer func() {
			client.didClose(req.absPath) //nolint:errcheck
			client.shutdown()
			client.stop()
		}()

		out, qErr := e.runLSPSession(client, req)
		done <- result{out: out, err: qErr}
	}()

	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.out, r.err
	case <-timer.C:
		client.stop()
		return "", fmt.Errorf("lsp_query: timed out after %ds", timeout)
	}
}
