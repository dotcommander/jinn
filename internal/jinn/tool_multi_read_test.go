package jinn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestMultiReadHappyPath(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "alpha\n")
	writeTestFile(t, dir, "b.txt", "bravo\n")
	writeTestFile(t, dir, "c.txt", "charlie\n")

	result, _, err := e.Dispatch(context.Background(), "multi_read", args("files", []interface{}{
		map[string]interface{}{"path": "a.txt"},
		map[string]interface{}{"path": "b.txt"},
		map[string]interface{}{"path": "c.txt"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var mr multiReadResult
	if err := json.Unmarshal([]byte(result.Text), &mr); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(mr.Files) != 3 {
		t.Errorf("expected 3 files, got %d", len(mr.Files))
	}
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		content, ok := mr.Files[name]
		if !ok {
			t.Errorf("missing file %q in result", name)
			continue
		}
		if !strings.Contains(content, name[:1]) {
			t.Errorf("expected content for %q to contain %q, got: %s", name, name[:1], content)
		}
	}
	if len(mr.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(mr.Errors), mr.Errors)
	}
}

func TestMultiReadEmptyFile(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "empty.txt", "")

	result, _, err := e.Dispatch(context.Background(), "multi_read", args("files", []interface{}{
		map[string]interface{}{"path": "empty.txt"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var mr multiReadResult
	if err := json.Unmarshal([]byte(result.Text), &mr); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	content, ok := mr.Files["empty.txt"]
	if !ok {
		t.Fatal("expected empty.txt in files")
	}
	if content != "" {
		t.Errorf("expected empty content, got: %q", content)
	}
	if len(mr.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(mr.Errors), mr.Errors)
	}
}

func TestMultiReadMixedSuccess(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "good1.txt", "content1\n")
	writeTestFile(t, dir, "good2.txt", "content2\n")

	result, _, err := e.Dispatch(context.Background(), "multi_read", args("files", []interface{}{
		map[string]interface{}{"path": "good1.txt"},
		map[string]interface{}{"path": "good2.txt"},
		map[string]interface{}{"path": "nonexistent.txt"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var mr multiReadResult
	if err := json.Unmarshal([]byte(result.Text), &mr); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(mr.Files) != 2 {
		t.Errorf("expected 2 files, got %d", len(mr.Files))
	}
	if len(mr.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(mr.Errors))
	}
	fileErr, ok := mr.Errors["nonexistent.txt"]
	if !ok {
		t.Fatal("expected error for nonexistent.txt")
	}
	if fileErr.ErrorCode != "file_not_found" {
		t.Errorf("expected error_code file_not_found, got %q", fileErr.ErrorCode)
	}
	if fileErr.Suggestion == "" {
		t.Error("expected non-empty suggestion")
	}
}

func TestMultiReadAllFail(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	_, _, err := e.Dispatch(context.Background(), "multi_read", args("files", []interface{}{
		map[string]interface{}{"path": "no1.txt"},
		map[string]interface{}{"path": "no2.txt"},
		map[string]interface{}{"path": "no3.txt"},
	}))
	if err == nil {
		t.Fatal("expected error when all files fail")
	}
	if !strings.Contains(err.Error(), "all 3 files failed") {
		t.Errorf("expected 'all 3 files failed', got: %v", err)
	}
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) {
		t.Fatalf("expected *ErrWithSuggestion, got %T: %v", err, err)
	}
}

func TestMultiReadWindowing(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	writeTestFile(t, dir, "windowed.txt", b.String())

	result, _, err := e.Dispatch(context.Background(), "multi_read", args("files", []interface{}{
		map[string]interface{}{"path": "windowed.txt", "start_line": float64(10), "end_line": float64(20)},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var mr multiReadResult
	if err := json.Unmarshal([]byte(result.Text), &mr); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	content, ok := mr.Files["windowed.txt"]
	if !ok {
		t.Fatal("expected windowed.txt in files")
	}
	if !strings.Contains(content, "line10") || !strings.Contains(content, "line20") {
		t.Errorf("expected lines 10-20, got: %s", content)
	}
	if strings.Contains(content, "line9") || strings.Contains(content, "line21") {
		t.Errorf("window should exclude line9 and line21, got: %s", content)
	}
}

func TestMultiReadCap(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	// Build 21 file requests.
	files := make([]interface{}, 21)
	for i := range 21 {
		files[i] = map[string]interface{}{"path": fmt.Sprintf("file%d.txt", i)}
	}

	_, _, err := e.Dispatch(context.Background(), "multi_read", args("files", files))
	if err == nil {
		t.Fatal("expected error for >20 files")
	}
	if !strings.Contains(err.Error(), "too many files") {
		t.Errorf("expected 'too many files', got: %v", err)
	}
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) {
		t.Fatalf("expected *ErrWithSuggestion, got %T: %v", err, err)
	}
	if sErr.Code != ErrCodeInvalidArgs {
		t.Errorf("expected error_code %q, got %q", ErrCodeInvalidArgs, sErr.Code)
	}
}

func TestMultiReadBinaryFile(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "text.txt", "hello world\n")
	// Write binary content with null bytes.
	writeTestFile(t, dir, "bin.dat", "hello\x00world\x00binary")

	result, _, err := e.Dispatch(context.Background(), "multi_read", args("files", []interface{}{
		map[string]interface{}{"path": "text.txt"},
		map[string]interface{}{"path": "bin.dat"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var mr multiReadResult
	if err := json.Unmarshal([]byte(result.Text), &mr); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(mr.Files) != 1 {
		t.Errorf("expected 1 file in files map, got %d", len(mr.Files))
	}
	if _, ok := mr.Files["text.txt"]; !ok {
		t.Error("expected text.txt in files map")
	}
	binErr, ok := mr.Errors["bin.dat"]
	if !ok {
		t.Fatal("expected bin.dat in errors map")
	}
	if binErr.ErrorCode != "binary_file" {
		t.Errorf("expected error_code binary_file, got %q", binErr.ErrorCode)
	}
}

func TestMultiReadTruncation(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var b strings.Builder
	for i := 1; i <= 2500; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	writeTestFile(t, dir, "big.txt", b.String())

	result, _, err := e.Dispatch(context.Background(), "multi_read", args("files", []interface{}{
		map[string]interface{}{"path": "big.txt"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var mr multiReadResult
	if err := json.Unmarshal([]byte(result.Text), &mr); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(mr.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(mr.Files))
	}
	trunc, ok := mr.Truncation["big.txt"]
	if !ok {
		t.Fatal("expected truncation metadata for big.txt")
	}
	if !trunc.Truncated {
		t.Error("expected Truncated=true")
	}
	if trunc.TotalLines != 2500 {
		t.Errorf("expected TotalLines=2500, got %d", trunc.TotalLines)
	}
	if trunc.OutputLines > 2000 {
		t.Errorf("expected OutputLines <= 2000, got %d", trunc.OutputLines)
	}
}

func TestMultiReadDuplicatePaths(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	writeTestFile(t, dir, "dup.txt", b.String())

	result, _, err := e.Dispatch(context.Background(), "multi_read", args("files", []interface{}{
		map[string]interface{}{"path": "dup.txt", "start_line": float64(1), "end_line": float64(5)},
		map[string]interface{}{"path": "dup.txt", "start_line": float64(10), "end_line": float64(15)},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var mr multiReadResult
	if err := json.Unmarshal([]byte(result.Text), &mr); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Last wins: only one entry for dup.txt with lines 10-15.
	if len(mr.Files) != 1 {
		t.Errorf("expected 1 file (last wins), got %d", len(mr.Files))
	}
	content, ok := mr.Files["dup.txt"]
	if !ok {
		t.Fatal("expected dup.txt in files map")
	}
	if !strings.Contains(content, "line10") {
		t.Errorf("expected second request (lines 10-15) to win, got: %s", content)
	}
	// The content should be from the second request (lines 10-15).
	// Check for line 5 content from the first request — it should NOT be present.
	if strings.Contains(content, "line5") {
		t.Errorf("expected first request to be overwritten (no line5), got: %s", content)
	}
	// No errors — duplicate paths are valid (last wins).
	if len(mr.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d", len(mr.Errors))
	}
}

func TestMultiReadEmptyFilesArray(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)

	_, _, err := e.Dispatch(context.Background(), "multi_read", args("files", []interface{}{}))
	if err == nil {
		t.Fatal("expected error for empty files array")
	}
	if !strings.Contains(err.Error(), "non-empty array") {
		t.Errorf("expected 'non-empty array' in error, got: %v", err)
	}
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) {
		t.Fatalf("expected *ErrWithSuggestion, got %T: %v", err, err)
	}
	if sErr.Code != ErrCodeInvalidArgs {
		t.Errorf("expected error_code %q, got %q", ErrCodeInvalidArgs, sErr.Code)
	}
}
