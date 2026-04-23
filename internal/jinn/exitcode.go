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
}

// exitTable maps command basename → exit code → classification entry.
// Commands absent from the table default to ClassError for non-zero exits.
var exitTable = map[string]map[int]exitEntry{
	"grep": {
		1: {ClassExpectedNonzero, "no matches found (grep exits 1 when the pattern does not match)"},
	},
	"rg": {
		1: {ClassExpectedNonzero, "no matches found (rg exits 1 when the pattern does not match)"},
	},
	"ag": {
		1: {ClassExpectedNonzero, "no matches found (ag exits 1 when the pattern does not match)"},
	},
	"diff": {
		1: {ClassExpectedNonzero, "files differ (diff exits 1 when inputs are not identical)"},
	},
	"cmp": {
		1: {ClassExpectedNonzero, "files differ (cmp exits 1 when inputs are not identical)"},
	},
	"test": {
		1: {ClassExpectedNonzero, "condition is false (test/[ exits 1 for a false expression)"},
	},
	"[": {
		1: {ClassExpectedNonzero, "condition is false ([ exits 1 for a false expression)"},
	},
	"curl": {
		22: {ClassExpectedNonzero, "HTTP error response (curl exits 22 for 4xx/5xx status codes)"},
	},
	"ssh": {
		255: {ClassError, "SSH connection or protocol error (exit 255 indicates a connection-level failure)"},
	},
	"timeout": {
		124: {ClassTimeout, "command exceeded time limit (timeout exits 124 when the child is killed)"},
	},
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
