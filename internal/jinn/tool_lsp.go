package jinn

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// lspTimeoutSec is the per-query timeout in seconds. Tests override this to
// keep test runs fast. It is only read once per lspQueryWithLauncher call.
var lspTimeoutSec = 10

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
	".go":  {[]string{"gopls", "serve"}, "go", "go install golang.org/x/tools/gopls@latest"},
	".rs":  {[]string{"rust-analyzer"}, "rust", "rustup component add rust-analyzer"},
	".py":  {[]string{"pylsp"}, "python", "pip install python-lsp-server"},
	".ts":  {[]string{"typescript-language-server", "--stdio"}, "typescript", "npm install -g typescript-language-server typescript"},
	".tsx": {[]string{"typescript-language-server", "--stdio"}, "typescriptreact", "npm install -g typescript-language-server typescript"},
	".js":  {[]string{"typescript-language-server", "--stdio"}, "javascript", "npm install -g typescript-language-server typescript"},
	".jsx": {[]string{"typescript-language-server", "--stdio"}, "javascriptreact", "npm install -g typescript-language-server typescript"},
}

// lspServerForExt returns the LSP server argv for ext.
// Returns ErrWithSuggestion when the extension is unknown or the binary is absent.
func lspServerForExt(ext string) ([]string, error) {
	e, ok := lspExtTable[ext]
	if !ok {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("no LSP server known for extension %s", ext),
			Suggestion: "supported extensions: .go, .rs, .py, .ts, .tsx, .js, .jsx",
		}
	}
	if _, err := exec.LookPath(e.argv[0]); err != nil {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("LSP server %q not found on PATH", e.argv[0]),
			Suggestion: fmt.Sprintf("install with: %s", e.install),
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

func (e *Engine) lspQuery(args map[string]interface{}) (string, error) {
	return e.lspQueryWithLauncher(args, nil)
}

// lspQueryWithLauncher is the testable variant — tests inject a fake launcher.
func (e *Engine) lspQueryWithLauncher(args map[string]interface{}, launcher lspLauncher) (string, error) {
	action, _ := args["action"].(string)
	path, _ := args["path"].(string)
	line := intArg(args, "line", 0)
	char := intArg(args, "character", 0)

	if action == "" {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("lsp_query: 'action' is required"),
			Suggestion: "set action to one of: definition, references, hover, symbols",
		}
	}
	if path == "" {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("lsp_query: 'path' is required"),
			Suggestion: "provide the path to the source file to query",
		}
	}
	absPath, err := e.checkPath(path)
	if err != nil {
		return "", err
	}

	ext := strings.ToLower(filepath.Ext(absPath))
	argv, err := lspServerForExt(ext)
	if err != nil {
		return "", err
	}

	// position required for all actions except symbols
	if action != "symbols" && (line <= 0 || char <= 0) {
		return "", &ErrWithSuggestion{
			Err:        fmt.Errorf("lsp_query: 'line' and 'character' are required for action %q", action),
			Suggestion: "provide 1-based line and character numbers for the symbol under the cursor",
		}
	}

	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)

	client := newLSPClient(launcher)
	if err := client.start(argv); err != nil {
		return "", err
	}

	go func() {
		defer func() {
			client.didClose(absPath) //nolint:errcheck
			client.shutdown()
			client.stop()
		}()

		if err := client.handshake(e.workDir); err != nil {
			done <- result{err: err}
			return
		}
		if err := client.didOpen(absPath, langIDForExt(ext)); err != nil {
			done <- result{err: err}
			return
		}

		var (
			out  string
			qErr error
		)
		switch action {
		case "definition":
			out, qErr = client.definition(absPath, line, char)
		case "references":
			out, qErr = client.references(absPath, line, char)
		case "hover":
			out, qErr = client.hover(absPath, line, char)
		case "symbols":
			out, qErr = client.symbols(absPath)
		default:
			qErr = &ErrWithSuggestion{
				Err:        fmt.Errorf("unknown lsp action: %s", action),
				Suggestion: "use one of: definition, references, hover, symbols",
			}
		}
		done <- result{out: out, err: qErr}
	}()

	select {
	case r := <-done:
		return r.out, r.err
	case <-time.After(time.Duration(lspTimeoutSec) * time.Second):
		client.stop()
		return "", fmt.Errorf("lsp_query: timed out after %ds", lspTimeoutSec)
	}
}
