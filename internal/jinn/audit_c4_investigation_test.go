package jinn

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuditC401HungServerProcess(t *testing.T) {
	if len(os.Args) > 1 && os.Args[len(os.Args)-1] == "jinn-audit-hung-lsp" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	t.Parallel()
}

type auditLSPWritePipe struct {
	io.WriteCloser
	closed  atomic.Bool
	cleanup chan struct{}
	once    sync.Once
}

func (pipe *auditLSPWritePipe) Close() error {
	pipe.closed.Store(true)
	return pipe.WriteCloser.Close()
}
func (pipe *auditLSPWritePipe) Write(data []byte) (int, error) {
	if pipe.closed.Load() {
		pipe.once.Do(func() { close(pipe.cleanup) })
	}
	return pipe.WriteCloser.Write(data)
}

type auditLSPReadPipe struct {
	io.ReadCloser
	closed atomic.Bool
}

func (pipe *auditLSPReadPipe) Close() error { pipe.closed.Store(true); return pipe.ReadCloser.Close() }

func TestAuditC401TimeoutCleanup(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("controlled subprocess integration")
	}
	for _, batch := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "batch"}[batch], func(t *testing.T) {
			t.Parallel()
			engine := New(t.TempDir(), "test")
			t.Cleanup(func() { _ = engine.Close() })
			engine.LSPTimeoutSec = 1
			writeLSPFile(t, engine.workDir, "source.go")
			var stdin *auditLSPWritePipe
			var stdout *auditLSPReadPipe
			reaped := make(chan struct{})
			launcher := func(ctx context.Context, _ []string) (lspProc, error) {
				executable, err := os.Executable()
				if err != nil {
					return lspProc{}, err
				}
				proc, err := launchLSP(ctx, []string{executable, "-test.run=^TestAuditC401HungServerProcess$", "--", "jinn-audit-hung-lsp"}, os.Environ())
				if err != nil {
					return lspProc{}, err
				}
				stdin = &auditLSPWritePipe{WriteCloser: proc.stdin, cleanup: make(chan struct{})}
				stdout = &auditLSPReadPipe{ReadCloser: proc.stdout}
				wait := proc.wait
				proc.stdin, proc.stdout = stdin, stdout
				proc.wait = func() error { err := wait(); close(reaped); return err }
				return proc, nil
			}
			if batch {
				out, err := engine.lspBatchWithLauncher(t.Context(), map[string]any{"queries": []any{lspArgs("action", "symbols", "path", "source.go")}}, launcher)
				if err != nil {
					t.Fatal(err)
				}
				var result lspBatchResult
				if err := json.Unmarshal([]byte(out), &result); err != nil {
					t.Fatal(err)
				}
				if result.Results[0].ErrorCode != ErrCodeTimeout {
					t.Fatalf("batch=%s", out)
				}
			} else {
				_, err := engine.lspQueryWithLauncher(t.Context(), lspArgs("action", "symbols", "path", "source.go"), launcher)
				if err == nil || !strings.Contains(err.Error(), "timed out") {
					t.Fatalf("single error=%v", err)
				}
			}
			for name, done := range map[string]<-chan struct{}{"reaped": reaped, "worker cleanup reached": stdin.cleanup} {
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatalf("%s blocked", name)
				}
			}
			if !stdin.closed.Load() || !stdout.closed.Load() {
				t.Fatal("process pipes remain open")
			}
		})
	}
}

func TestAuditC4R01DiskSnapshots(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture; no native Windows claim")
	}
	engine := New(t.TempDir(), "test")
	t.Cleanup(func() { _ = engine.Close() })
	snapshot := "package demo\n// disk snapshot\n"
	writeTestFile(t, engine.workDir, "source.go", snapshot)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gopls"), []byte("#!/bin/sh\n/bin/cat \"$2\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	engine.execPath = []string{bin}
	args := lspArgs("action", "diagnostics", "path", "source.go")
	single, err := engine.lspQuery(t.Context(), args)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := engine.lspBatch(t.Context(), map[string]any{"queries": []any{args}})
	if err != nil {
		t.Fatal(err)
	}
	var result lspBatchResult
	if err := json.Unmarshal([]byte(batch), &result); err != nil {
		t.Fatal(err)
	}
	if result.Results[0].Result != single || !strings.Contains(single, strings.TrimSpace(snapshot)) {
		t.Fatalf("single=%q, batch=%s", single, batch)
	}
	opened := make(chan string, 1)
	if _, err := engine.lspQueryWithLauncher(t.Context(), args, newMockLauncherCfg(mockConfig{openedText: opened})); err != nil {
		t.Fatal(err)
	}
	if got := <-opened; got != snapshot {
		t.Fatalf("opened snapshot=%q", got)
	}
}
