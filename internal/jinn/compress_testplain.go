package jinn

import (
	"fmt"
	"strings"
)

// isGoTestPlainOutput reports whether output looks like non-verbose `go test ./...`
// output: has per-package result lines but no `=== RUN` markers (which would mean -v).
func isGoTestPlainOutput(output string) bool {
	if strings.Contains(output, "=== RUN") {
		return false
	}
	for _, line := range splitLines(output) {
		if reGoTestOK.MatchString(line) || reGoTestFailPkg.MatchString(line) {
			return true
		}
	}
	return false
}

// compressGoTestPlain compresses non-verbose `go test ./...` output.
// All packages pass -> single summary line. Any failure -> keep only the
// signal (failure blocks, panics, FAIL lines), dropping per-package `ok`
// lines, `?  [no test files]` lines, and any stray PASS/RUN noise.
func (s *testResultStrategy) compressGoTestPlain(output string) string {
	lines := splitLines(output)
	okPkgs, noTestPkgs, hasFailure := scanGoTestPlainLines(lines)

	if !hasFailure && okPkgs > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "✓ %d package(s) ok", okPkgs)
		if noTestPkgs > 0 {
			fmt.Fprintf(&b, " (%d with no test files)", noTestPkgs)
		}
		result := b.String()
		if len(result) >= len(output) {
			return output
		}
		return result
	}

	// Failures present: drop only lines we are certain are noise; keep everything else.
	var kept []string
	for _, line := range lines {
		if isGoTestPlainNoise(line) {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		return output
	}
	result := strings.Join(kept, "\n")
	if len(result) >= len(output) {
		return output
	}
	return result
}

// scanGoTestPlainLines classifies non-verbose `go test ./...` lines into
// passing-package and no-test-file counts, plus whether any failure appears.
func scanGoTestPlainLines(lines []string) (okPkgs, noTestPkgs int, hasFailure bool) {
	for _, line := range lines {
		switch {
		case reGoTestOK.MatchString(line):
			okPkgs++
		case strings.HasPrefix(line, "?") && strings.Contains(line, "[no test files]"):
			noTestPkgs++
		case reGoTestFail.MatchString(line) || reGoTestFailPkg.MatchString(line) ||
			strings.HasPrefix(line, "--- FAIL:") || line == "FAIL" || strings.HasPrefix(line, "panic:"):
			hasFailure = true
		}
	}
	return okPkgs, noTestPkgs, hasFailure
}

// isGoTestPlainNoise reports whether a line is certain-to-be-noise in plain
// `go test ./...` output: passing-package `ok` lines, `[no test files]` lines,
// and stray -v leftovers (RUN/PASS).
func isGoTestPlainNoise(line string) bool {
	if reGoTestOK.MatchString(line) {
		return true // "ok  \tpkg\t0.2s" — a passing package
	}
	if strings.HasPrefix(line, "?") && strings.Contains(line, "[no test files]") {
		return true
	}
	// -v leftovers, shouldn't appear in plain output but be safe.
	return reGoTestRun.MatchString(line) || reGoTestPass.MatchString(line) || line == "PASS"
}
