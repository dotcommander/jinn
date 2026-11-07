package jinn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parseListResult(t *testing.T, raw string) listDirResult {
	t.Helper()
	var res listDirResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("list_dir returned invalid JSON: %v\nraw: %s", err, raw)
	}
	return res
}

func TestListDir_SymlinkTargets(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "real.txt", "content")
	if err := os.Symlink("real.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	result, err := e.listDir(args("depth", float64(1)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	res := parseListResult(t, result)
	if res.Symlinks["link.txt"] != "real.txt" {
		t.Fatalf("expected symlink metadata for link.txt, got: %+v", res.Symlinks)
	}
}

func TestListDir_TruncationReasonDepth(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	deep := dir
	for i := 0; i < 12; i++ {
		deep = filepath.Join(deep, "d")
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, dir, filepath.Join(deep, "marker.txt"), "x")
	result, err := e.listDir(args("depth", float64(10)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	res := parseListResult(t, result)
	if !res.Truncated {
		t.Fatalf("expected truncation at the depth cap, got: %+v", res)
	}
	if res.TruncationReason != listTruncDepth {
		t.Errorf("expected truncation_reason=%q, got %q", listTruncDepth, res.TruncationReason)
	}
	if !strings.Contains(res.Hint, "depth budget") {
		t.Errorf("expected a depth hint naming the cap, got: %s", res.Hint)
	}
}

func TestListDir_TruncationReasonMaxEntries(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a.txt", "x")
	writeTestFile(t, dir, "b.txt", "x")
	writeTestFile(t, dir, "c.txt", "x")
	result, err := e.listDir(args("max_entries", float64(2)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	res := parseListResult(t, result)
	if !res.Truncated {
		t.Fatalf("expected truncation, got: %+v", res)
	}
	if res.TruncationReason != listTruncMaxEntries {
		t.Errorf("expected truncation_reason=%q, got %q", listTruncMaxEntries, res.TruncationReason)
	}
}

func TestListDir_NoTruncationNoReason(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "only.txt", "x")
	result, err := e.listDir(args())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	res := parseListResult(t, result)
	if res.Truncated || res.TruncationReason != "" {
		t.Errorf("no truncation expected, got: %+v", res)
	}
	if res.Symlinks != nil {
		t.Errorf("no symlinks expected, got: %+v", res.Symlinks)
	}
}
