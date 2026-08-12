package jinn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileReturnsMachineContinuation(t *testing.T) {
	t.Parallel()
	engine := New(t.TempDir(), "test")
	t.Cleanup(func() { _ = engine.Close() })
	if err := os.WriteFile(filepath.Join(engine.workDir, "long.txt"), []byte(strings.Repeat("line\n", readDefaultLines+5)), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	result, err := engine.readFile(map[string]interface{}{"path": "long.txt"})
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	next, ok := result.Meta["next_call"].(*NextCall)
	if !ok || next.Tool != "read_file" || next.Arguments["start_line"] != readDefaultLines+1 {
		t.Fatalf("next_call = %#v", result.Meta["next_call"])
	}
}

func TestReadFileOmitsUnsafeTailContinuation(t *testing.T) {
	t.Parallel()
	engine := New(t.TempDir(), "test")
	t.Cleanup(func() { _ = engine.Close() })
	line := strings.Repeat("x", 100) + "\n"
	if err := os.WriteFile(filepath.Join(engine.workDir, "long.txt"), []byte(strings.Repeat(line, readDefaultLines+5)), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	result, err := engine.readFile(map[string]interface{}{"path": "long.txt", "truncate": "tail"})
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if _, ok := result.Meta["next_call"]; ok {
		t.Fatalf("tail result exposed unsafe next_call: %#v", result.Meta)
	}
}

func TestListDirContinuationAdvancesOffset(t *testing.T) {
	t.Parallel()
	engine := New(t.TempDir(), "test")
	t.Cleanup(func() { _ = engine.Close() })
	writeContinuationFiles(t, engine.workDir)
	first := decodeListResult(t, engine, map[string]interface{}{"path": ".", "depth": 1, "max_entries": 1})
	if first.NextCall == nil || first.NextCall.Arguments["offset"] != float64(1) && first.NextCall.Arguments["offset"] != 1 {
		t.Fatalf("first next_call = %#v", first.NextCall)
	}
	second := decodeListResult(t, engine, first.NextCall.Arguments)
	if len(first.Entries) != 1 || len(second.Entries) != 1 || first.Entries[0] == second.Entries[0] || second.Offset != 1 {
		t.Fatalf("pages = first:%+v second:%+v", first, second)
	}
}

func TestFindFilesContinuationAdvancesOffset(t *testing.T) {
	t.Parallel()
	engine := New(t.TempDir(), "test")
	t.Cleanup(func() { _ = engine.Close() })
	writeContinuationFiles(t, engine.workDir)
	first := decodeFindResult(t, engine, map[string]interface{}{"pattern": "*.txt", "path": ".", "limit": 1})
	if first.NextCall == nil {
		t.Fatal("missing find next_call")
	}
	second := decodeFindResult(t, engine, first.NextCall.Arguments)
	if len(first.Files) != 1 || len(second.Files) != 1 || first.Files[0] == second.Files[0] || second.Offset != 1 {
		t.Fatalf("pages = first:%+v second:%+v", first, second)
	}
}

func TestSearchFilesContinuationAdvancesOffset(t *testing.T) {
	t.Parallel()
	engine := New(t.TempDir(), "test")
	t.Cleanup(func() { _ = engine.Close() })
	if err := os.WriteFile(filepath.Join(engine.workDir, "matches.txt"), []byte("needle one\nneedle two\nneedle three\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	first := decodeSearchResult(t, engine, map[string]interface{}{"pattern": "needle", "path": ".", "format": "json", "max_matches": 1})
	if first.NextCall == nil {
		t.Fatal("missing search next_call")
	}
	second := decodeSearchResult(t, engine, first.NextCall.Arguments)
	if len(first.Results) != 1 || len(second.Results) != 1 || first.Results[0].Line == second.Results[0].Line || second.Offset != 1 {
		t.Fatalf("pages = first:%+v second:%+v", first, second)
	}
}

func TestMultiReadReturnsNextCalls(t *testing.T) {
	t.Parallel()
	engine := New(t.TempDir(), "test")
	t.Cleanup(func() { _ = engine.Close() })
	if err := os.WriteFile(filepath.Join(engine.workDir, "long.txt"), []byte(strings.Repeat("line\n", readDefaultLines+5)), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	result, err := engine.multiRead(map[string]interface{}{"files": []interface{}{map[string]interface{}{"path": "long.txt"}}})
	if err != nil {
		t.Fatalf("multiRead: %v", err)
	}
	var decoded multiReadResult
	if err := json.Unmarshal([]byte(result.Text), &decoded); err != nil {
		t.Fatalf("decode multi_read: %v", err)
	}
	if len(decoded.NextCalls) != 1 || decoded.NextCalls[0].Tool != "read_file" {
		t.Fatalf("next_calls = %#v", decoded.NextCalls)
	}
}

func writeContinuationFiles(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func decodeListResult(t *testing.T, engine *Engine, args map[string]interface{}) listDirResult {
	t.Helper()
	output, err := engine.listDirContext(t.Context(), args)
	if err != nil {
		t.Fatalf("listDirContext: %v", err)
	}
	var result listDirResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return result
}

func decodeFindResult(t *testing.T, engine *Engine, args map[string]interface{}) findFilesResult {
	t.Helper()
	output, err := engine.findFiles(t.Context(), args)
	if err != nil {
		t.Fatalf("findFiles: %v", err)
	}
	var result findFilesResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode find: %v", err)
	}
	return result
}

func decodeSearchResult(t *testing.T, engine *Engine, args map[string]interface{}) searchFilesResult {
	t.Helper()
	output, err := engine.searchFilesContext(t.Context(), args)
	if err != nil {
		t.Fatalf("searchFilesContext: %v", err)
	}
	var result searchFilesResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	return result
}
