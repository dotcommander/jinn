//go:build !windows

package jinn

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// POSIX FIFOs do not exist as filesystem entries on Windows.
func TestReadFileRejectsFIFOWithoutBlocking(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if _, err := e.readFile(args("path", "pipe")); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("FIFO read error = %v", err)
	}
}
