package jinn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFile_Basic(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "test.txt", "line1\nline2\nline3\n")
	result, err := e.readFile(args("path", "test.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "1\tline1") || !strings.Contains(result, "3\tline3") {
		t.Errorf("expected line-numbered output, got: %s", result)
	}
}

func TestReadFile_NotFound(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.readFile(args("path", "nonexistent.txt"))
	if err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Errorf("expected 'file not found' error, got: %v", err)
	}
}

func TestReadFile_Directory(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	os.Mkdir(filepath.Join(dir, "subdir"), 0o755)
	_, err := e.readFile(args("path", "subdir"))
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("expected 'not a regular file' error, got: %v", err)
	}
}

func TestReadFile_OffsetPastEnd(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "small.txt", "one\ntwo\n")
	_, err := e.readFile(args("path", "small.txt", "start_line", float64(999)))
	if err == nil || !strings.Contains(err.Error(), "past end") {
		t.Errorf("expected 'past end' error, got: %v", err)
	}
}

func TestReadFile_BinaryDetection(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "bin.dat", "hello\x00world")
	result, err := e.readFile(args("path", "bin.dat"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "binary file") {
		t.Errorf("expected 'binary file', got: %s", result)
	}
}

func TestReadFile_AlignedLineNumbers(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var content strings.Builder
	for i := range 120 {
		fmt.Fprintf(&content, "line %d\n", i+1)
	}
	writeTestFile(t, dir, "long.txt", content.String())
	result, err := e.readFile(args("path", "long.txt", "start_line", float64(1), "end_line", float64(120)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "  1\t") {
		t.Errorf("expected right-aligned line numbers, first 80 chars: %s", result[:min(80, len(result))])
	}
}

func TestReadFile_Window(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var content strings.Builder
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&content, "line%d\n", i)
	}
	writeTestFile(t, dir, "ten.txt", content.String())
	result, err := e.readFile(args("path", "ten.txt", "start_line", float64(3), "end_line", float64(5)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "line3") {
		t.Errorf("expected line3, got: %s", result)
	}
	if strings.Contains(result, "line2") || strings.Contains(result, "line6") {
		t.Errorf("window should exclude line2 and line6, got: %s", result)
	}
}

func TestReadFile_SensitivePath(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.readFile(args("path", ".git/config"))
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Errorf("expected blocked error, got: %v", err)
	}
}

func TestReadFile_Tail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		total  int
		tail   int
		want   string // must appear in output
		noWant string // must NOT appear
	}{
		{"last 3 of 10", 10, 3, "8\tline8", "line7"},
		{"tail exceeds file", 5, 100, "1\tline1", ""},
		{"tail 1", 10, 1, "10\tline10", "line9"},
		{"tail 0 disabled", 10, 0, "1\tline1", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, dir := testEngine(t)
			var b strings.Builder
			for i := 1; i <= tc.total; i++ {
				fmt.Fprintf(&b, "line%d\n", i)
			}
			writeTestFile(t, dir, "tail.txt", b.String())
			result, err := e.readFile(args("path", "tail.txt", "tail", float64(tc.tail)))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(result, tc.want) {
				t.Errorf("expected %q in output, got:\n%s", tc.want, result)
			}
			if tc.noWant != "" && strings.Contains(result, tc.noWant) {
				t.Errorf("expected %q NOT in output, got:\n%s", tc.noWant, result)
			}
		})
	}
}

func TestReadFile_TailTakesPrecedence(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	writeTestFile(t, dir, "prec.txt", b.String())
	result, err := e.readFile(args("path", "prec.txt", "tail", float64(3), "start_line", float64(1), "end_line", float64(5)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "18\tline18") {
		t.Errorf("tail should override start_line/end_line, got:\n%s", result)
	}
	resultLines := strings.Split(result, "\n")
	for _, l := range resultLines {
		if l == "1\tline1" || l == "5\tline5" {
			t.Errorf("start_line/end_line should be ignored when tail is set, got:\n%s", result)
			break
		}
	}
}

func TestReadFile_ContinuationHint(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var content strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&content, "line%d\n", i)
	}
	writeTestFile(t, dir, "big.txt", content.String())
	result, err := e.readFile(args("path", "big.txt", "start_line", float64(1), "end_line", float64(10)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Use start_line=11 to continue") {
		t.Errorf("expected continuation hint, got: %s", result)
	}
}
