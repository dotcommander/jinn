package jinn

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// lspProc is the result of launching an LSP server: the pipes to drive it, a
// kill func to terminate the process, and a wait func to reap it. Returned by
// lspLauncher.
type lspProc struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	kill   func() error
	wait   func() error
}

// lspLauncher launches an LSP server and returns its stdin write end, stdout
// read end, and a kill function. Tests inject a fake launcher over pipes.
type lspLauncher func(ctx context.Context, argv []string) (lspProc, error)

func realLauncher(ctx context.Context, argv []string) (lspProc, error) {
	return launchLSP(ctx, argv, subprocessEnv(nil))
}

func launchLSP(ctx context.Context, argv, env []string) (lspProc, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // G204: argv built internally from a fixed server table
	cmd.Stderr = io.Discard                               // suppress LSP server stderr noise
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return lspProc{}, fmt.Errorf("lsp stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return lspProc{}, fmt.Errorf("lsp stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return lspProc{}, fmt.Errorf("lsp start: %w", err)
	}
	return lspProc{
		stdin:  stdin,
		stdout: stdout,
		kill:   func() error { return cmd.Process.Kill() },
		wait:   cmd.Wait,
	}, nil
}

// lspClient drives one LSP session. All calls are synchronous and single-threaded;
// the LSP handshake and query sequence does not require concurrent requests.
type lspClient struct {
	stdin           io.WriteCloser
	stdoutRaw       io.ReadCloser // underlying stdout closer; closed in stop() to unblock a blocked read
	stdout          *bufio.Reader
	kill            func() error
	wait            func() error
	nextID          atomic.Int64
	launcher        lspLauncher // nil → use realLauncher
	stopOnce        sync.Once
	pushDiagnostics map[string][]lspDiagnostic
}

func newLSPClient(launcher lspLauncher) *lspClient {
	return &lspClient{launcher: launcher, pushDiagnostics: make(map[string][]lspDiagnostic)}
}

func (c *lspClient) start(ctx context.Context, argv []string) error {
	launch := c.launcher
	if launch == nil {
		launch = realLauncher
	}
	proc, err := launch(ctx, argv)
	if err != nil {
		return err
	}
	c.stdin = proc.stdin
	c.stdoutRaw = proc.stdout
	c.stdout = bufio.NewReader(proc.stdout)
	c.kill = proc.kill
	c.wait = proc.wait
	return nil
}

func (c *lspClient) stop() {
	c.stopOnce.Do(func() {
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.stdoutRaw != nil {
			_ = c.stdoutRaw.Close()
		}
		if c.kill != nil {
			_ = c.kill()
		}
		if c.wait != nil {
			_ = c.wait()
		}
	})
}

// --- LSP lifecycle ---

func (c *lspClient) handshake(workDir string) error {
	type clientInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	type workspaceFolder struct {
		URI  string `json:"uri"`
		Name string `json:"name"`
	}
	type initParams struct {
		ProcessID        int               `json:"processId"`
		RootPath         string            `json:"rootPath,omitempty"`
		RootURI          string            `json:"rootUri"`
		WorkspaceFolders []workspaceFolder `json:"workspaceFolders,omitempty"`
		Capabilities     map[string]any    `json:"capabilities"`
		ClientInfo       clientInfo        `json:"clientInfo"`
	}
	rootURI := pathToURI(workDir)
	_, err := c.sendRequest("initialize", initParams{
		ProcessID: os.Getpid(),
		RootPath:  workDir,
		RootURI:   rootURI,
		WorkspaceFolders: []workspaceFolder{{
			URI:  rootURI,
			Name: filepath.Base(workDir),
		}},
		Capabilities: map[string]any{
			"textDocument": map[string]any{
				"diagnostic":         map[string]any{"dynamicRegistration": false},
				"publishDiagnostics": map[string]any{},
			},
		},
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

func (c *lspClient) didOpenText(absPath, langID string, data []byte) error {
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

func (c *lspClient) shutdownBounded() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.shutdown()
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		c.stop()
		<-done
	}
}
