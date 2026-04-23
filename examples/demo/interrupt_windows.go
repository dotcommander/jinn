//go:build windows

package main

import "context"

// startEscInterrupt is a no-op on Windows. The demo compiles and runs, but
// Esc does not interrupt dispatch. Ctrl-C continues to work via the signal
// handler in repl.go.
func startEscInterrupt(parent context.Context) (context.Context, func()) {
	return parent, func() {}
}
