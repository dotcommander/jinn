package jinn

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLSPBatchSharesServerAndPreservesOrder(t *testing.T) {
	t.Parallel()
	engine := New(t.TempDir(), "test")
	t.Cleanup(func() { _ = engine.Close() })
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(engine.workDir, name), []byte("package demo\n"), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	var starts atomic.Int32
	opened := make(chan string, 4)
	base := newMockLauncherCfg(mockConfig{openedText: opened})
	launcher := func(ctx context.Context, argv []string) (lspProc, error) {
		starts.Add(1)
		return base(ctx, argv)
	}
	output, err := engine.lspBatchWithLauncher(t.Context(), map[string]interface{}{
		"queries": []interface{}{
			map[string]interface{}{"action": "symbols", "path": "a.go"},
			map[string]interface{}{"action": "symbols", "path": "b.go"},
			map[string]interface{}{"action": "symbols", "path": "./a.go"},
			map[string]interface{}{"action": "symbols", "path": "b.go"},
		},
	}, launcher)
	if err != nil {
		t.Fatalf("lspBatchWithLauncher: %v", err)
	}
	var result lspBatchResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if starts.Load() != 1 || result.ServerStarts != 1 || result.Succeeded != 4 || result.Failed != 0 {
		t.Fatalf("batch counts = starts:%d result:%+v", starts.Load(), result)
	}
	if len(opened) != 2 {
		t.Fatalf("didOpen count = %d, want 2 (one per canonical file)", len(opened))
	}
	for index, item := range result.Results {
		if item.Index != index || !item.OK || item.Result == "" {
			t.Fatalf("result[%d] = %+v", index, item)
		}
	}
}

func TestLSPBatchParallelServersPreserveOrderAndErrors(t *testing.T) {
	t.Parallel()
	engine := New(t.TempDir(), "test")
	t.Cleanup(func() { _ = engine.Close() })
	for name, content := range map[string]string{"a.go": "package demo\n", "b.go": "package demo\n", "c.py": "pass\n"} {
		if err := os.WriteFile(filepath.Join(engine.workDir, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	started := make(chan string, 2)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	base := newMockLauncher(false)
	launcher := func(ctx context.Context, argv []string) (lspProc, error) {
		started <- argv[0]
		select {
		case <-release:
		case <-ctx.Done():
			return lspProc{}, ctx.Err()
		}
		if argv[0] == "pylsp" {
			return lspProc{}, errors.New("python server unavailable")
		}
		return base(ctx, argv)
	}
	type batchOutput struct {
		output string
		err    error
	}
	finished := make(chan batchOutput, 1)
	go func() {
		output, err := engine.lspBatchWithLauncher(ctx, map[string]interface{}{
			"queries": []interface{}{
				map[string]interface{}{"action": "symbols", "path": "a.go"},
				map[string]interface{}{"action": "symbols", "path": "c.py"},
				map[string]interface{}{"action": "symbols", "path": "b.go"},
			},
		}, launcher)
		finished <- batchOutput{output, err}
	}()

	seen := make(map[string]bool)
	for range 2 {
		select {
		case server := <-started:
			seen[server] = true
		case <-ctx.Done():
			t.Fatal("distinct server groups did not start concurrently")
		}
	}
	if !seen["gopls"] || !seen["pylsp"] {
		t.Fatalf("started servers = %v", seen)
	}
	once.Do(func() { close(release) })
	var completed batchOutput
	select {
	case completed = <-finished:
	case <-ctx.Done():
		t.Fatal("batch did not finish")
	}
	if completed.err != nil {
		t.Fatalf("lspBatchWithLauncher: %v", completed.err)
	}
	var result lspBatchResult
	if err := json.Unmarshal([]byte(completed.output), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.ServerStarts != 2 || result.Succeeded != 2 || result.Failed != 1 {
		t.Fatalf("batch counts = %+v", result)
	}
	for index, item := range result.Results {
		if item.Index != index {
			t.Fatalf("result[%d] index = %d", index, item.Index)
		}
		if index == 1 {
			if item.OK || item.Error != "python server unavailable" {
				t.Fatalf("result[%d] = %+v", index, item)
			}
		} else if !item.OK || item.Result == "" {
			t.Fatalf("result[%d] = %+v", index, item)
		}
	}
}

func TestLSPBatchPreservesPartialErrors(t *testing.T) {
	t.Parallel()
	engine := New(t.TempDir(), "test")
	t.Cleanup(func() { _ = engine.Close() })
	if err := os.WriteFile(filepath.Join(engine.workDir, "main.go"), []byte("package demo\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	output, err := engine.lspBatchWithLauncher(t.Context(), map[string]interface{}{
		"queries": []interface{}{
			map[string]interface{}{"action": "symbols", "path": "main.go"},
			map[string]interface{}{"action": "invalid", "path": "main.go"},
		},
	}, newMockLauncher(false))
	if err != nil {
		t.Fatalf("lspBatchWithLauncher: %v", err)
	}
	var result lspBatchResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Succeeded != 1 || result.Failed != 1 || !result.Results[0].OK || result.Results[1].OK || result.Results[1].ErrorCode != ErrCodeInvalidArgs {
		t.Fatalf("partial result = %+v", result)
	}
}
