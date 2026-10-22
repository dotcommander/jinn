package jinn

import (
	"encoding/json"
	"fmt"
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

func TestReadExplicitWindowContinuation(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"read_file", "multi_read"} {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			engine := New(t.TempDir(), "test")
			t.Cleanup(func() { _ = engine.Close() })
			if err := os.WriteFile(filepath.Join(engine.workDir, "window.txt"), []byte("one\ntwo\nthree\nfour\nfive\nsix\nseven\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			args := map[string]interface{}{"path": "window.txt", "start_line": float64(2), "end_line": float64(3), "line_numbers": false}
			var next *NextCall
			if tool == "read_file" {
				result, err := engine.readFile(args)
				if err != nil {
					t.Fatal(err)
				}
				next, _ = result.Meta["next_call"].(*NextCall)
			} else {
				result, err := engine.multiRead(map[string]interface{}{"files": []interface{}{args}})
				if err != nil {
					t.Fatal(err)
				}
				var decoded multiReadResult
				if err := json.Unmarshal([]byte(result.Text), &decoded); err != nil {
					t.Fatal(err)
				}
				if len(decoded.NextCalls) != 1 {
					t.Fatalf("next calls = %#v", decoded.NextCalls)
				}
				next = &decoded.NextCalls[0]
			}
			if next == nil {
				t.Fatal("missing continuation")
			}
			encoded, err := json.Marshal(next)
			if err != nil {
				t.Fatal(err)
			}
			var replay NextCall
			if err := json.Unmarshal(encoded, &replay); err != nil {
				t.Fatal(err)
			}
			if replay.Arguments["start_line"] != float64(4) || replay.Arguments["end_line"] != float64(5) {
				t.Fatalf("continuation window = %#v", replay.Arguments)
			}
			result, err := engine.readFile(replay.Arguments)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(result.Text, "four\nfive\n[Showing lines 4-5 of 7.") {
				t.Fatalf("replayed content = %q", result.Text)
			}
		})
	}
}

func TestReadHeadHintsUseSourceWindow(t *testing.T) {
	t.Parallel()
	engine := New(t.TempDir(), "test")
	t.Cleanup(func() { _ = engine.Close() })
	var fixture strings.Builder
	for line := 1; line <= readTruncLines+6; line++ {
		fmt.Fprintf(&fixture, "line %d\n", line)
	}
	if err := os.WriteFile(filepath.Join(engine.workDir, "window.txt"), []byte(fixture.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := engine.readFile(map[string]interface{}{"path": "window.txt", "start_line": float64(4), "end_line": float64(readTruncLines + 4), "line_numbers": false})
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("[Showing lines 4-%d of %d. Use start_line=%d to continue.", readTruncLines+3, readTruncLines+6, readTruncLines+4)
	if !strings.Contains(result.Text, want) || strings.Contains(result.Text, "[truncated:") {
		t.Fatalf("source hint missing or relative hint retained: %q", result.Text)
	}
	next, _ := result.Meta["next_call"].(*NextCall)
	if next == nil || next.Arguments["end_line"] != readTruncLines+4 {
		t.Fatalf("unexhausted upper bound = %#v", next)
	}
	tr := applyTruncateStrategy(strings.Repeat("line\n", readTruncLines-1)+"line", "head", ".txt")
	if tr.Truncated || strings.Contains(tr.Content, "[truncated:") {
		t.Fatal("exact-fit head output reports truncation")
	}
}

func TestSearchJSONStablePagination(t *testing.T) {
	t.Parallel()
	engine := New(t.TempDir(), "test")
	t.Cleanup(func() { _ = engine.Close() })
	pages := []string{
		"ws/notes.txt:2:third payload here\nws/alpha/one.txt:1:alpha one payload\nws/alpha/two.txt:1:alpha two payload\n",
		"ws/alpha/one.txt:1:alpha one payload\nws/alpha/two.txt:1:alpha two payload\nws/notes.txt:2:third payload here\n",
	}
	var files []string
	for page, raw := range pages {
		encoded, err := engine.formatSearchJSON(raw, searchRequest{offset: page * 2, maxMatches: 2}, map[string]interface{}{"pattern": "payload", "path": "ws"})
		if err != nil {
			t.Fatal(err)
		}
		var result searchFilesResult
		if err := json.Unmarshal([]byte(encoded), &result); err != nil {
			t.Fatal(err)
		}
		for _, match := range result.Results {
			files = append(files, match.File)
		}
		if page == 1 && (result.NextCall != nil || !result.TotalCountExact) {
			t.Fatalf("last page = %#v", result)
		}
	}
	if got := strings.Join(files, ","); got != "ws/alpha/one.txt,ws/alpha/two.txt,ws/notes.txt" {
		t.Fatalf("paged paths = %s", got)
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
