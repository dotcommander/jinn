package main_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

// Logging-policy guard: jinn must never write non-JSON to stdout and must
// write nothing to stderr, because the agent caller parses stdout as the
// JSON wire protocol. This test fails if any logging (e.g. an accidental
// slog handler) leaks onto either stream.
func TestWireProtocol_StdoutIsJSON_StderrEmpty(t *testing.T) {
	t.Parallel()

	bin := filepath.Join(t.TempDir(), "jinn-test-bin")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build jinn: %v\n%s", err, out)
	}

	cmd := exec.Command(bin)
	cmd.Stdin = bytes.NewReader([]byte(`{"tool":"read_file","args":{"path":"main.go"}}`))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Exit code is not asserted: success and tool-level error both produce
	// a single JSON object on stdout; only stream discipline matters here.
	_ = cmd.Run()

	if stderr.Len() != 0 {
		t.Fatalf("stderr must be empty (wire-protocol invariant), got: %q", stderr.String())
	}
	var v map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &v); err != nil {
		t.Fatalf("stdout must be valid JSON (wire-protocol invariant): %v\nstdout: %q", err, stdout.String())
	}
}
