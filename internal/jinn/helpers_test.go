package jinn

import (
	"os"
	"path/filepath"
	"testing"
)

func testEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	return New(dir), dir
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
