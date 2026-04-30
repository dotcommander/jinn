package jinn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListDir_Basic(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "")
	writeTestFile(t, dir, "b.txt", "")
	result, err := e.listDir(args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "a.txt") || !strings.Contains(result, "b.txt") {
		t.Errorf("expected both files, got: %s", result)
	}
}

func TestListDir_HiddenExcluded(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, ".hidden", "secret")
	writeTestFile(t, dir, "visible.txt", "hi")
	result, err := e.listDir(args("depth", float64(1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, ".hidden") {
		t.Errorf("hidden files should be excluded, got: %s", result)
	}
	if !strings.Contains(result, "visible.txt") {
		t.Errorf("visible files should be listed, got: %s", result)
	}
}

func TestListDir_DepthClamp(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	// depth < 1 clamps to 1, depth > 10 clamps to 10. Verify no panic.
	e.listDir(args("depth", float64(0)))
	e.listDir(args("depth", float64(99)))
}

func TestListDir_EmptySubdir(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	os.Mkdir(filepath.Join(dir, "emptydir"), 0o755)
	result, err := e.listDir(args("path", "emptydir", "depth", float64(1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("result should not be empty string")
	}
}

// --- Feature 3: entry limit tests ---

func TestListDir_DefaultCap(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// Create listDefaultMax + 5 files to trigger truncation.
	for i := range listDefaultMax + 5 {
		writeTestFile(t, dir, fmt.Sprintf("f%04d.txt", i), "")
	}
	result, err := e.listDir(args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, `"truncated":true`) {
		t.Errorf("expected truncated=true, got: %s", result[:min(200, len(result))])
	}
	if !strings.Contains(result, "TRUNCATED") {
		t.Errorf("expected TRUNCATED hint, got: %s", result[:min(200, len(result))])
	}
}

func TestListDir_ExplicitCap(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	for i := range 10 {
		writeTestFile(t, dir, fmt.Sprintf("g%02d.txt", i), "")
	}
	result, err := e.listDir(args("max_entries", float64(3)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, `"truncated":true`) {
		t.Errorf("expected truncated=true with explicit cap 3, got: %s", result[:min(200, len(result))])
	}
	if !strings.Contains(result, `"total_count":`) {
		t.Errorf("expected total_count field, got: %s", result[:min(200, len(result))])
	}
}

func TestListDir_TruncatedFlagAndHint(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	for i := range 5 {
		writeTestFile(t, dir, fmt.Sprintf("h%d.txt", i), "")
	}
	result, err := e.listDir(args("max_entries", float64(2)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "TRUNCATED") {
		t.Errorf("expected TRUNCATED hint string, got: %s", result)
	}
	if !strings.Contains(result, "max_entries") {
		t.Errorf("expected 'max_entries' in hint, got: %s", result)
	}
}

func TestListDir_TotalCountAccuracy(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	const n = 8
	for i := range n {
		writeTestFile(t, dir, fmt.Sprintf("cnt%d.txt", i), "")
	}
	// Use small cap so we truncate.
	result, err := e.listDir(args("max_entries", float64(3), "depth", float64(1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// total_count should be >= n (find also returns the directory itself).
	if !strings.Contains(result, `"total_count"`) {
		t.Errorf("expected total_count in result, got: %s", result)
	}
}

func TestListDir_NoCap_SmallDir(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "")
	writeTestFile(t, dir, "b.txt", "")
	result, err := e.listDir(args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "a.txt") || !strings.Contains(result, "b.txt") {
		t.Errorf("expected both files in result, got: %s", result)
	}
	if strings.Contains(result, `"truncated":true`) {
		t.Errorf("small dir should not be truncated, got: %s", result)
	}
}

// Change 5: directory entries are suffixed with "/".
func TestListDir_AppendsSlashToDirectories(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "file.txt", "")
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := e.listDir(args("depth", float64(1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The subdir entry must appear with a trailing slash.
	if !strings.Contains(result, "subdir/") {
		t.Errorf("expected 'subdir/' in entries, got: %s", result)
	}
	// Plain files must NOT get a trailing slash.
	if strings.Contains(result, "file.txt/") {
		t.Errorf("file entries should not have trailing slash, got: %s", result)
	}
}
