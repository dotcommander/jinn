package jinn

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// testResultStrategy: go test verbose markers.
	reGoTestRun     = regexp.MustCompile(`^=== RUN   `)
	reGoTestPass    = regexp.MustCompile(`^--- PASS: `)
	reGoTestFail    = regexp.MustCompile(`^--- FAIL: `)
	reGoTestOK      = regexp.MustCompile(`^ok\s+\S+`)
	reGoTestFailPkg = regexp.MustCompile(`^FAIL\s+\S+`)
)

// ---------------------------------------------------------------------------
// testResultStrategy
// ---------------------------------------------------------------------------

// testResultStrategy compresses verbose test runner output when all tests pass.
// Supports go test, pytest, cargo test, and jest/vitest patterns.
type testResultStrategy struct{}

func (s *testResultStrategy) Name() string { return "test_result" }

func (s *testResultStrategy) AppliesTo(output string, tool string) bool {
	return countTestLines(output) >= 5
}

func (s *testResultStrategy) Compress(output string) string {
	switch {
	case isGoTestOutput(output):
		return s.compressGoTest(output)
	case isGoTestPlainOutput(output):
		return s.compressGoTestPlain(output)
	case isPytestOutput(output):
		return s.compressPytest(output)
	case isCargoTestOutput(output):
		return s.compressCargoTest(output)
	default:
		return output
	}
}

// goTestSummary holds the tallies and metadata scanned from go test -v output.
type goTestSummary struct {
	passCount, failCount int
	pkg, duration        string
}

// compressGoTest compresses go test -v output when all tests pass.
func (s *testResultStrategy) compressGoTest(output string) string {
	t := scanGoTestLines(splitLines(output))

	// Only compress when all tests pass.
	if t.failCount > 0 || t.passCount == 0 {
		return output
	}

	result := formatTestSummary(t.pkg, t.passCount, t.duration)
	if len(result) >= len(output) {
		return output
	}
	return result
}

// scanGoTestLines tallies pass/fail counts and extracts the package name and
// duration from the `ok` line of go test -v output.
func scanGoTestLines(lines []string) goTestSummary {
	var t goTestSummary
	for _, line := range lines {
		if reGoTestPass.MatchString(line) {
			t.passCount++
		}
		if reGoTestFail.MatchString(line) {
			t.failCount++
		}
		if reGoTestOK.MatchString(line) {
			t.pkg, t.duration = parseGoTestOKLine(line)
		}
	}
	return t
}

// parseGoTestOKLine extracts the package name and duration from a go test
// `ok  pkg  0.00s` line.
func parseGoTestOKLine(line string) (pkg, duration string) {
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		pkg = fields[1]
	}
	// Extract duration (last field matching "0.00s" pattern).
	for i := len(fields) - 1; i >= 0; i-- {
		f := fields[i]
		if strings.HasSuffix(f, "s") && len(f) > 1 && f[0] >= '0' && f[0] <= '9' {
			duration = f
			break
		}
	}
	return pkg, duration
}

// compressPytest compresses pytest -v output when all tests pass.
func (s *testResultStrategy) compressPytest(output string) string {
	lines := splitLines(output)
	passCount := 0
	failCount := 0
	var duration string

	for _, line := range lines {
		if strings.Contains(line, " PASSED") {
			passCount++
		}
		if strings.Contains(line, " FAILED") || strings.Contains(line, " ERROR") {
			failCount++
		}
		// Summary line: "3 passed in 0.01s" or "3 passed, 1 failed in 0.01s".
		if idx := strings.LastIndex(line, " in "); idx >= 0 && strings.Contains(line, "passed") {
			duration = strings.TrimSpace(strings.TrimSuffix(line[idx+4:], "."))
		}
	}

	if failCount > 0 || passCount == 0 {
		return output
	}

	result := formatTestSummary("", passCount, duration)
	if len(result) >= len(output) {
		return output
	}
	return result
}

// compressCargoTest compresses cargo test output when all tests pass.
func (s *testResultStrategy) compressCargoTest(output string) string {
	for _, line := range splitLines(output) {
		if !strings.HasPrefix(line, "test result: ") {
			continue
		}
		// "test result: ok. 3 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s"
		if !strings.Contains(line, "ok.") || !strings.Contains(line, "0 failed") {
			continue
		}
		passCount, duration := parseCargoResultLine(line)
		if passCount > 0 {
			result := formatTestSummary("", passCount, duration)
			if len(result) < len(output) {
				return result
			}
		}
		break
	}
	return output
}

// parseCargoResultLine extracts the pass count and duration from a cargo
// "test result: ok. N passed; ... finished in 0.00s" line.
func parseCargoResultLine(line string) (passCount int, duration string) {
	if idx := strings.Index(line, ". "); idx >= 0 {
		rest := line[idx+2:]
		if pIdx := strings.Index(rest, " passed"); pIdx >= 0 {
			if n, _ := fmt.Sscanf(rest[:pIdx], "%d", &passCount); n != 1 {
				passCount = 0
			}
		}
	}
	if idx := strings.Index(line, "finished in "); idx >= 0 {
		d := strings.TrimSpace(line[idx+12:])
		if !strings.HasSuffix(d, "s") {
			d += "s"
		}
		duration = d
	}
	return passCount, duration
}

// formatTestSummary builds a compact test result summary line.
func formatTestSummary(pkg string, passed int, duration string) string {
	var b strings.Builder
	b.WriteString("✓ ")
	if pkg != "" {
		b.WriteString(pkg)
		b.WriteByte(' ')
	}
	fmt.Fprintf(&b, "%d passed", passed)
	if duration != "" {
		fmt.Fprintf(&b, " (%s)", duration)
	}
	return b.String()
}

// countTestLines counts lines matching known test runner patterns.
func countTestLines(output string) int {
	lines := splitLines(output)
	count := 0
	for _, line := range lines {
		if isTestOutputLine(line) {
			count++
		}
	}
	return count
}

// isTestOutputLine returns true if the line matches a known test runner pattern.
func isTestOutputLine(line string) bool {
	// go test
	if reGoTestRun.MatchString(line) || reGoTestPass.MatchString(line) || reGoTestFail.MatchString(line) {
		return true
	}
	if line == "PASS" || line == "FAIL" {
		return true
	}
	if reGoTestOK.MatchString(line) || reGoTestFailPkg.MatchString(line) {
		return true
	}
	// pytest
	if strings.Contains(line, "PASSED") || strings.Contains(line, "FAILED") {
		return true
	}
	if strings.Contains(line, "test session starts") {
		return true
	}
	// cargo test
	if strings.Contains(line, "test result:") || strings.HasPrefix(line, "running ") {
		return true
	}
	return false
}

// isGoTestOutput returns true if the output looks like go test -v output.
func isGoTestOutput(output string) bool {
	return strings.Contains(output, "=== RUN") &&
		(strings.Contains(output, "--- PASS") || strings.Contains(output, "--- FAIL"))
}

// isPytestOutput returns true if the output looks like pytest output.
func isPytestOutput(output string) bool {
	return strings.Contains(output, "PASSED") || strings.Contains(output, "FAILED") ||
		strings.Contains(output, "test session starts")
}

// isCargoTestOutput returns true if the output looks like cargo test output.
func isCargoTestOutput(output string) bool {
	return strings.Contains(output, "test result:")
}
