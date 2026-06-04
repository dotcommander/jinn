package jinn

import (
	"fmt"
	"regexp"
	"strings"
)

// Strategy is a compression strategy that can be applied to tool output.
type Strategy interface {
	// Name returns a unique identifier for this strategy (used in metadata).
	Name() string
	// AppliesTo returns true if this strategy should run on the given output.
	// The tool name is provided so strategies can be tool-specific.
	AppliesTo(output string, tool string) bool
	// Compress applies the strategy to the output and returns the compressed version.
	// Must be deterministic: same input always produces same output.
	// Must be lossless for signal: never drop error messages, test failures, or diff hunks.
	Compress(output string) string
}

// CompressionMeta carries metadata about what compression was applied.
type CompressionMeta struct {
	Strategies  []string `json:"strategies,omitempty"`
	OriginalLen int      `json:"original_len,omitempty"`
	FinalLen    int      `json:"final_len,omitempty"`
}

// Compressor applies a chain of strategies to tool output.
type Compressor struct {
	strategies []Strategy
}

// NewCompressor creates a Compressor with the default strategy chain.
func NewCompressor() *Compressor {
	return &Compressor{
		strategies: []Strategy{
			&pathPrefixStrategy{},
			&hashAbbrevStrategy{},
			&testResultStrategy{},
			&buildOutputStrategy{},
			&gitStatusStrategy{},
		},
	}
}

// defaultCompressor is a shared, stateless Compressor used by run_shell.
var defaultCompressor = NewCompressor()

// Compress applies all applicable strategies to the output.
// It returns the compressed text and metadata about what was applied.
// If compression panics, the original output is returned (fail-open).
func (c *Compressor) Compress(output string, tool string) (result string, meta CompressionMeta) {
	meta.OriginalLen = len(output)

	// Fail-open: if any strategy panics, return the original output unchanged.
	defer func() {
		if r := recover(); r != nil {
			result = output
			meta = CompressionMeta{
				OriginalLen: len(output),
				FinalLen:    len(output),
			}
		}
	}()

	result = output
	for _, s := range c.strategies {
		if s.AppliesTo(result, tool) {
			result = s.Compress(result)
			meta.Strategies = append(meta.Strategies, s.Name())
		}
	}
	meta.FinalLen = len(result)
	return result, meta
}

// ---------------------------------------------------------------------------
// Compiled regex patterns (package-level to avoid per-call compilation).
// ---------------------------------------------------------------------------

var (
	// hashAbbrevStrategy: 40-character hex SHA hashes at word boundaries.
	reFullHash = regexp.MustCompile(`\b[0-9a-f]{40}\b`)

	// testResultStrategy: go test verbose markers.
	reGoTestRun     = regexp.MustCompile(`^=== RUN   `)
	reGoTestPass    = regexp.MustCompile(`^--- PASS: `)
	reGoTestFail    = regexp.MustCompile(`^--- FAIL: `)
	reGoTestOK      = regexp.MustCompile(`^ok\s+\S+`)
	reGoTestFailPkg = regexp.MustCompile(`^FAIL\s+\S+`)

	// buildOutputStrategy: build tool markers.
	reGoBuildPkg = regexp.MustCompile(`^# \S+`)
	reGoModOp    = regexp.MustCompile(`^go: `)
	reCargoBuild = regexp.MustCompile(`^Compiling\s+\S+`)
	reNpmAdded   = regexp.MustCompile(`^added \d+ packages?`)
	reMakeLine   = regexp.MustCompile(`^make\[\d+\]:`)

	// gitStatusStrategy: status entry and tracking patterns.
	reGitStatusEntry = regexp.MustCompile(`^\t(both modified|new file|modified|deleted|renamed):\s+(.*)`)
	reGitTracking    = regexp.MustCompile(`Your branch is (up to date with|ahead of|behind) '([^']+)'`)
	reGitByCount     = regexp.MustCompile(`by (\d+)`)
)

// ---------------------------------------------------------------------------
// pathPrefixStrategy
// ---------------------------------------------------------------------------

// pathPrefixStrategy detects when 3+ lines share a common directory prefix
// and factors it out into a [cwd: ...] header line.
type pathPrefixStrategy struct{}

func (s *pathPrefixStrategy) Name() string { return "path_prefix_dedup" }

func (s *pathPrefixStrategy) AppliesTo(output string, tool string) bool {
	pathCount := 0
	for _, line := range splitLines(output) {
		if isPathLine(line) {
			pathCount++
			if pathCount >= 3 {
				return true
			}
		}
	}
	return false
}

func (s *pathPrefixStrategy) Compress(output string) string {
	lines := splitLines(output)
	if len(lines) == 0 {
		return output
	}

	// Collect trimmed path-like lines for prefix computation.
	var pathLines []string
	for _, line := range lines {
		t := strings.TrimLeft(line, " \t")
		if isPathStr(t) {
			pathLines = append(pathLines, t)
		}
	}
	if len(pathLines) < 3 {
		return output
	}

	prefix := longestCommonPathPrefix(pathLines)
	if prefix == "" {
		return output
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[cwd: %s]", strings.TrimSuffix(prefix, "/"))

	count := 0
	for _, line := range lines {
		b.WriteByte('\n')
		t := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(t, prefix) {
			count++
			leading := line[:len(line)-len(t)]
			relative := t[len(prefix):]
			if relative == "" {
				relative = "."
			}
			b.WriteString(leading)
			b.WriteString(relative)
		} else {
			b.WriteString(line)
		}
	}

	if count < 3 {
		return output
	}
	result := b.String()
	if len(result) >= len(output) {
		return output
	}
	return result
}

// isPathLine returns true if the line starts with an absolute or relative path.
func isPathLine(line string) bool {
	return isPathStr(strings.TrimLeft(line, " \t"))
}

// isPathStr returns true if s looks like a file path (starts with / or ./).
func isPathStr(s string) bool {
	return (strings.HasPrefix(s, "/") && len(s) > 1) || strings.HasPrefix(s, "./")
}

// longestCommonPathPrefix finds the longest common directory prefix among
// the given paths. Returns empty string if no meaningful common prefix exists.
func longestCommonPathPrefix(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	prefix := paths[0]
	for _, p := range paths[1:] {
		for !strings.HasPrefix(p, prefix) {
			idx := strings.LastIndex(prefix, "/")
			if idx <= 0 {
				return ""
			}
			prefix = prefix[:idx+1] // Keep trailing slash for directory boundary.
		}
		if prefix == "" {
			return ""
		}
	}
	// Ensure the prefix ends at a clean directory boundary.
	if !strings.HasSuffix(prefix, "/") {
		idx := strings.LastIndex(prefix, "/")
		if idx <= 0 {
			return ""
		}
		prefix = prefix[:idx+1]
	}
	// Reject trivially short prefixes.
	if prefix == "/" || prefix == "./" {
		return ""
	}
	return prefix
}

// ---------------------------------------------------------------------------
// hashAbbrevStrategy
// ---------------------------------------------------------------------------

// hashAbbrevStrategy abbreviates full 40-char hex SHA hashes to 8 characters.
type hashAbbrevStrategy struct{}

func (s *hashAbbrevStrategy) Name() string { return "hash_abbrev" }

func (s *hashAbbrevStrategy) AppliesTo(output string, tool string) bool {
	return len(reFullHash.FindAllString(output, -1)) >= 2
}

func (s *hashAbbrevStrategy) Compress(output string) string {
	result := reFullHash.ReplaceAllStringFunc(output, func(hash string) string {
		return hash[:8]
	})
	if len(result) >= len(output) {
		return output
	}
	return result
}

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

// isPytestOutput returns true if the output looks like pytest output.
func isPytestOutput(output string) bool {
	return strings.Contains(output, "PASSED") || strings.Contains(output, "FAILED") ||
		strings.Contains(output, "test session starts")
}

// isCargoTestOutput returns true if the output looks like cargo test output.
func isCargoTestOutput(output string) bool {
	return strings.Contains(output, "test result:")
}

// ---------------------------------------------------------------------------
// buildOutputStrategy
// ---------------------------------------------------------------------------

// buildOutputStrategy compresses build command output. When the build succeeds
// with no errors or warnings, it collapses to a single line. When there are
// issues, it keeps them but removes boilerplate (download lines, etc.).
type buildOutputStrategy struct{}

func (s *buildOutputStrategy) Name() string { return "build_output" }

func (s *buildOutputStrategy) AppliesTo(output string, tool string) bool {
	return isBuildOutput(output) && len(splitLines(output)) >= 5
}

func (s *buildOutputStrategy) Compress(output string) string {
	lines := splitLines(output)

	// Check if all lines are build progress (no errors or warnings).
	allProgress := true
	for _, line := range lines {
		if !isBuildProgressLine(line) {
			allProgress = false
			break
		}
	}

	if allProgress {
		tool := detectBuildTool(output)
		result := fmt.Sprintf("✓ %s (no issues)", tool)
		if len(result) >= len(output) {
			return output
		}
		return result
	}

	// Has issues: keep non-boilerplate lines (errors, warnings, context).
	var kept []string
	for _, line := range lines {
		if !isBuildBoilerplate(line) {
			kept = append(kept, line)
		}
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

// isBuildOutput returns true if the output looks like build command output.
func isBuildOutput(output string) bool {
	lines := splitLines(output)
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if reGoBuildPkg.MatchString(t) || reGoModOp.MatchString(t) ||
			reCargoBuild.MatchString(t) || strings.HasPrefix(t, "npm ") ||
			reMakeLine.MatchString(t) {
			return true
		}
	}
	return false
}

// isBuildProgressLine returns true if the line is build progress output
// (not an error or warning).
func isBuildProgressLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return true
	}
	// go build -v package markers: "# pkg".
	if reGoBuildPkg.MatchString(t) {
		return true
	}
	// go module operations (but not errors/warnings).
	if reGoModOp.MatchString(t) && !containsErrorOrWarning(t) {
		return true
	}
	// cargo compiling.
	if reCargoBuild.MatchString(t) {
		return true
	}
	// cargo Finished.
	if strings.HasPrefix(t, "Finished ") {
		return true
	}
	// npm install success.
	if reNpmAdded.MatchString(t) {
		return true
	}
	if strings.HasPrefix(t, "removed ") && strings.Contains(t, "packages") {
		return true
	}
	// make enter/leave directory.
	if reMakeLine.MatchString(t) {
		return true
	}
	// Generic "Building ...".
	if strings.HasPrefix(t, "Building ") {
		return true
	}
	return false
}

// isBuildBoilerplate returns true if the line is safe-to-remove build noise.
func isBuildBoilerplate(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return true
	}
	// go module operations (but not errors/warnings).
	if reGoModOp.MatchString(t) && !containsErrorOrWarning(t) {
		return true
	}
	// cargo compilation progress.
	if reCargoBuild.MatchString(t) {
		return true
	}
	// make directory changes.
	if reMakeLine.MatchString(t) {
		return true
	}
	return false
}

// containsErrorOrWarning returns true if s contains error or warning indicators.
func containsErrorOrWarning(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "error") || strings.Contains(lower, "warning")
}

// detectBuildTool returns the name of the build tool that produced the output.
func detectBuildTool(output string) string {
	if strings.Contains(output, "go: ") || reGoBuildPkg.MatchString(output) {
		return "go build"
	}
	if strings.Contains(output, "Compiling ") {
		return "cargo build"
	}
	if strings.Contains(output, "npm ") {
		return "npm"
	}
	if reMakeLine.MatchString(output) {
		return "make"
	}
	return "build"
}

// ---------------------------------------------------------------------------
// gitStatusStrategy
// ---------------------------------------------------------------------------

// gitStatusStrategy compresses verbose git status output to a compact
// branch + file summary format.
type gitStatusStrategy struct{}

func (s *gitStatusStrategy) Name() string { return "git_status" }

func (s *gitStatusStrategy) AppliesTo(output string, tool string) bool {
	hasBranch := strings.HasPrefix(output, "On branch ") || strings.HasPrefix(output, "HEAD detached")
	hasChanges := strings.Contains(output, "Changes") || strings.Contains(output, "Untracked")
	return hasBranch && hasChanges
}

func (s *gitStatusStrategy) Compress(output string) string {
	lines := splitLines(output)
	if len(lines) == 0 {
		return output
	}

	var b strings.Builder

	// First line is branch info.
	b.WriteString(lines[0])

	// Look for tracking info on subsequent lines.
	for _, line := range lines {
		if m := reGitTracking.FindStringSubmatch(line); m != nil {
			direction := m[1]
			ref := m[2]
			switch direction {
			case "up to date with":
				fmt.Fprintf(&b, " (up to date with %s)", ref)
			case "ahead of":
				s.writeAheadBehind(&b, "ahead", ref, line)
			case "behind":
				s.writeAheadBehind(&b, "behind", ref, line)
			}
			break
		}
	}

	entries := parseGitEntries(lines)

	if len(entries) > 0 {
		b.WriteByte('\n')
		b.WriteString(strings.Join(entries, "  "))
	}

	result := b.String()
	if len(result) >= len(output) {
		return output
	}
	return result
}

// parseGitEntries scans git status lines section by section, returning the
// compact per-file entries (status-char prefix + name, or "+name" for untracked).
func parseGitEntries(lines []string) []string {
	var entries []string
	section := ""
	for _, line := range lines {
		switch {
		case strings.Contains(line, "Changes to be committed:"):
			section = "staged"
		case strings.Contains(line, "Changes not staged for commit:"):
			section = "unstaged"
		case strings.Contains(line, "Untracked files:"):
			section = "untracked"
		case strings.HasPrefix(line, "\t"):
			if section == "untracked" {
				name := line[1:] // Trim leading tab.
				if name != "" {
					entries = append(entries, "+"+name)
				}
			} else if m := reGitStatusEntry.FindStringSubmatch(line); m != nil {
				entries = append(entries, gitStatusChar(m[1])+m[2])
			}
		}
	}
	return entries
}

// writeAheadBehind appends ahead/behind tracking info to the builder.
func (s *gitStatusStrategy) writeAheadBehind(b *strings.Builder, direction, ref, line string) {
	if cm := reGitByCount.FindStringSubmatch(line); cm != nil {
		fmt.Fprintf(b, " (%s %s of %s)", direction, cm[1], ref)
	} else {
		fmt.Fprintf(b, " (%s %s)", direction, ref)
	}
}

// gitStatusChar maps a git status word to a single-character prefix code.
func gitStatusChar(status string) string {
	switch status {
	case "modified":
		return "M "
	case "deleted":
		return "D "
	case "new file":
		return "A "
	case "renamed":
		return "R "
	case "both modified":
		return "C "
	default:
		return "? "
	}
}
