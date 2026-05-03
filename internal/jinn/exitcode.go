package jinn

import (
	"path/filepath"
	"strings"
)

// Classification describes how a shell exit code should be interpreted by the
// calling LLM. Expected-nonzero exits are semantic signals, not failures.
type Classification string

const (
	// ClassSuccess means exit 0 — command completed normally.
	ClassSuccess Classification = "success"
	// ClassExpectedNonzero means a non-zero exit that is a semantic signal
	// (e.g., grep exit 1 = no matches). The LLM should NOT retry.
	ClassExpectedNonzero Classification = "expected_nonzero"
	// ClassError means an unexpected non-zero exit indicating failure.
	ClassError Classification = "error"
	// ClassTimeout means the command exceeded its time limit (exit 124).
	ClassTimeout Classification = "timeout"
	// ClassSignal means the process was killed by a signal.
	ClassSignal Classification = "signal"
)

type exitEntry struct {
	class  Classification
	reason string
	hint   string
}

// exitTable maps command basename → exit code → classification entry.
// Commands absent from the table default to ClassError for non-zero exits.
var exitTable = map[string]map[int]exitEntry{
	"grep": {
		1: {ClassExpectedNonzero, "no matches found (grep exits 1 when the pattern does not match)", ""},
	},
	"rg": {
		1: {ClassExpectedNonzero, "no matches found (rg exits 1 when the pattern does not match)", ""},
	},
	"ag": {
		1: {ClassExpectedNonzero, "no matches found (ag exits 1 when the pattern does not match)", ""},
	},
	"diff": {
		1: {ClassExpectedNonzero, "files differ (diff exits 1 when inputs are not identical)", ""},
	},
	"cmp": {
		1: {ClassExpectedNonzero, "files differ (cmp exits 1 when inputs are not identical)", ""},
	},
	"test": {
		1: {ClassExpectedNonzero, "condition is false (test/[ exits 1 for a false expression)", ""},
	},
	"[": {
		1: {ClassExpectedNonzero, "condition is false ([ exits 1 for a false expression)", ""},
	},
	"curl": {
		22: {ClassExpectedNonzero, "HTTP error response (curl exits 22 for 4xx/5xx status codes)", ""},
	},
	"ssh": {
		255: {ClassError, "SSH connection or protocol error (exit 255 indicates a connection-level failure)", ""},
	},
	"timeout": {
		124: {ClassTimeout, "command exceeded time limit (timeout exits 124 when the child is killed)", ""},
	},
}

// stderrHints maps common stderr patterns to actionable recovery hints.
// Matched case-insensitively against the combined stderr output.
var stderrHints = []struct {
	pattern string
	hint    string
}{
	{"go: no go.mod file", "run: go mod init <module-path>"},
	{"command not found", "install the missing command or check PATH"},
	{"permission denied", "check file permissions or run with appropriate access"},
	{"No such file or directory", "verify the file path exists"},
	{"EADDRINUSE", "port is in use -- kill the process or choose a different port"},
	{"EACCES", "check file permissions or run with appropriate access"},
	{"npm ERR! peer dep", "run: npm install --legacy-peer-deps"},
	{"Cannot find module", "run: npm install (or bun install)"},
	{"ModuleNotFoundError", "run: pip install <missing-module>"},
	{"cargo: command not found", "install Rust via rustup"},
	{"go: module", "run: go mod tidy"},
	{"connection refused", "target service is not running or wrong port"},
	{"certificate", "check TLS certificates or use --insecure for testing"},
	{"out of memory", "increase available memory or reduce workload"},
	{"disk full", "free disk space"},
	{"too many open files", "increase ulimit: ulimit -n 4096"},
	{"killed", "process was killed (OOM or signal) -- check system resources"},
}

// matchStderrHint returns the first matching recovery hint for the given
// stderr output, or "" if no pattern matches. Matching is case-insensitive.
func matchStderrHint(stderr string) string {
	lower := strings.ToLower(stderr)
	for _, h := range stderrHints {
		if strings.Contains(lower, strings.ToLower(h.pattern)) {
			return h.hint
		}
	}
	return ""
}

// classifyExitCode returns the classification and a human-readable reason for
// the given command basename and exit code. Negative or >128 exit codes
// indicate signal termination.
func classifyExitCode(argv0 string, exitCode int) (Classification, string) {
	if exitCode == 0 {
		return ClassSuccess, "command completed successfully"
	}
	// Signal termination: negative exit code or >128 (128+signum convention).
	if exitCode < 0 || exitCode > 128 {
		return ClassSignal, "process was terminated by a signal"
	}
	// exit 124 means timeout regardless of argv0: when jinn wraps a command
	// with `timeout bash -c ...`, argv0 resolves to `bash`, not `timeout`,
	// so the table lookup would miss. Catch the signal directly.
	if exitCode == 124 {
		return ClassTimeout, "command exceeded time limit"
	}

	base := strings.ToLower(filepath.Base(argv0))
	if codes, ok := exitTable[base]; ok {
		if entry, ok := codes[exitCode]; ok {
			return entry.class, entry.reason
		}
	}
	return ClassError, "command exited with non-zero status"
}

// extractArgv0 returns the basename of the first whitespace-delimited token
// in cmd. Quoted commands are not parsed — this is a best-effort extraction
// that accepts the limitation for v1 (see roadmap open questions).
func extractArgv0(cmd string) string {
	cmd = strings.TrimLeft(cmd, " \t")
	end := strings.IndexAny(cmd, " \t")
	if end < 0 {
		return filepath.Base(cmd)
	}
	return filepath.Base(cmd[:end])
}
