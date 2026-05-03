package jinn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks so the engine's workDir matches paths returned by
	// filepath.EvalSymlinks in checkPath (critical on macOS where /var ->
	// /private/var).
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return New(dir, "dev"), dir
}

func args(kv ...any) map[string]interface{} {
	m := make(map[string]interface{}, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// parseFindResult extracts the JSON portion (before any [TRUNCATED] hint) and unmarshals it.
func parseFindResult(t *testing.T, raw string) findFilesResult {
	t.Helper()
	jsonPart := raw
	if idx := strings.Index(raw, "\n["); idx >= 0 {
		jsonPart = raw[:idx]
	}
	var res findFilesResult
	if err := json.Unmarshal([]byte(jsonPart), &res); err != nil {
		t.Fatalf("invalid JSON: %v\nfull result: %s", err, raw)
	}
	return res
}
