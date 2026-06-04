package jinn

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	// buildOutputStrategy: build tool markers.
	reGoBuildPkg = regexp.MustCompile(`^# \S+`)
	reGoModOp    = regexp.MustCompile(`^go: `)
	reCargoBuild = regexp.MustCompile(`^Compiling\s+\S+`)
	reNpmAdded   = regexp.MustCompile(`^added \d+ packages?`)
	reMakeLine   = regexp.MustCompile(`^make\[\d+\]:`)
)

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
