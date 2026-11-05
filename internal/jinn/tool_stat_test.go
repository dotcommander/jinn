package jinn

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditC205PartialSampleDoesNotClaimLines(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "large.txt", strings.Repeat("a\n", 5000))
	encoded, err := e.statFile(args("path", "large.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		t.Fatal(err)
	}
	if result["sample_complete"] != false || result["sample_lines"] == nil || result["lines"] != nil || strings.Contains(result["summary"].(string), "lines:") {
		t.Fatalf("partial count misrepresented: %s", encoded)
	}
}

func TestStatFile_Regular(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "stat.txt", "one\ntwo\nthree\n")
	result, err := e.statFile(args("path", "stat.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "type: file") {
		t.Errorf("expected type: file, got: %s", result)
	}
	if !strings.Contains(result, "lines: 3") {
		t.Errorf("expected lines: 3, got: %s", result)
	}
	if !strings.Contains(result, "modified:") {
		t.Errorf("expected modified timestamp, got: %s", result)
	}
	if !strings.Contains(result, "encoding: utf-8") {
		t.Errorf("expected encoding: utf-8, got: %s", result)
	}
	if !strings.Contains(result, "line_ending: lf") {
		t.Errorf("expected line_ending: lf, got: %s", result)
	}
	if !strings.Contains(result, "bom: none") {
		t.Errorf("expected bom: none, got: %s", result)
	}
}

func TestStatFile_Directory(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	_ = os.Mkdir(filepath.Join(dir, "statdir"), 0o755)
	result, err := e.statFile(args("path", "statdir"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "type: directory") {
		t.Errorf("expected type: directory, got: %s", result)
	}
	// directories should not have encoding/line_ending/bom fields
	if strings.Contains(result, "encoding:") {
		t.Errorf("directory should not have encoding field, got: %s", result)
	}
}

func TestStatFile_NotFound(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.statFile(args("path", "ghost.txt"))
	if err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Errorf("expected 'file not found' error, got: %v", err)
	}
	var ews *ErrWithSuggestion
	if !errors.As(err, &ews) {
		t.Fatalf("expected *ErrWithSuggestion, got %T", err)
	}
	if ews.Code != ErrCodeFileNotFound {
		t.Errorf("expected code %q, got %q", ErrCodeFileNotFound, ews.Code)
	}
}

func TestStatFile_CRLF(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "crlf.txt", "one\r\ntwo\r\nthree\r\n")
	result, err := e.statFile(args("path", "crlf.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "line_ending: crlf") {
		t.Errorf("expected line_ending: crlf, got: %s", result)
	}
}

func TestStatFile_BOM(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	content := append([]byte{0xEF, 0xBB, 0xBF}, []byte("hello\n")...)
	writeTestFile(t, dir, "bom.txt", string(content))
	result, err := e.statFile(args("path", "bom.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "bom: utf-8-bom") {
		t.Errorf("expected bom: utf-8-bom, got: %s", result)
	}
}

func TestStatFile_Binary(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// Write invalid UTF-8 bytes
	dir2 := filepath.Join(dir, "bin.dat")
	_ = os.WriteFile(dir2, []byte{0x80, 0x81, 0x82, 0x83}, 0o644)
	result, err := e.statFile(args("path", "bin.dat"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "encoding: binary") {
		t.Errorf("expected encoding: binary, got: %s", result)
	}
}

func TestStatFile_MixedLineEndings(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "mixed.txt", "one\r\ntwo\nthree\r\n")
	result, err := e.statFile(args("path", "mixed.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "line_ending: mixed") {
		t.Errorf("expected line_ending: mixed, got: %s", result)
	}
}
