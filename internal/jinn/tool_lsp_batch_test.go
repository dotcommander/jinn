package jinn

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
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
	base := newMockLauncher(false)
	launcher := func(ctx context.Context, argv []string) (lspProc, error) {
		starts.Add(1)
		return base(ctx, argv)
	}
	output, err := engine.lspBatchWithLauncher(t.Context(), map[string]interface{}{
		"queries": []interface{}{
			map[string]interface{}{"action": "symbols", "path": "a.go"},
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
	if starts.Load() != 1 || result.ServerStarts != 1 || result.Succeeded != 2 || result.Failed != 0 {
		t.Fatalf("batch counts = starts:%d result:%+v", starts.Load(), result)
	}
	for index, item := range result.Results {
		if item.Index != index || !item.OK || item.Result == "" {
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
