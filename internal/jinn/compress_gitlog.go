package jinn

import (
	"regexp"
	"strings"
)

// reGitLogCommitLine matches the "commit <hash> (optional decoration)" line
// produced by git log's medium format.
var reGitLogCommitLine = regexp.MustCompile(`^commit ([0-9a-f]{7,40})(?: \((.+)\))?\s*$`)

// reGitDiffstatSummary matches the diffstat summary line, e.g. " 3 files changed, 10 insertions(+)".
var reGitDiffstatSummary = regexp.MustCompile(`^\s*\d+ files? changed`)

// condenseGitLog collapses medium-format git log output to one line per commit:
//
//	<hash8> [(decoration)] [merge] subject line
//
// It bails (returns output unchanged) when the input:
//   - Contains a diff (`diff --git ` or `@@ ` hunk lines) — e.g. git log -p
//   - Contains a diffstat summary (`\d+ files? changed`) — e.g. git log --stat
//   - Does not appear to be medium-format git log output at all
func condenseGitLog(output string) string {
	lines := splitLines(output)
	if !isCondensableGitLog(output, lines) {
		return output
	}

	var acc gitLogAccumulator
	for _, line := range lines {
		acc.consume(line)
	}
	acc.flush()

	if len(acc.emitted) == 0 {
		return output
	}
	result := strings.Join(acc.emitted, "\n")
	if len(result) >= len(output) {
		return output
	}
	return result
}

// isCondensableGitLog reports whether output is medium-format git log we can
// condense: it must contain at least one commit line and must NOT contain a
// diff (`diff --git`, `@@ ` hunks) or a diffstat summary.
func isCondensableGitLog(output string, lines []string) bool {
	if strings.Contains(output, "diff --git ") {
		return false
	}
	hasCommitLine := false
	for _, line := range lines {
		if strings.HasPrefix(line, "@@ ") {
			return false
		}
		if reGitDiffstatSummary.MatchString(line) {
			return false
		}
		if !hasCommitLine && reGitLogCommitLine.MatchString(line) {
			hasCommitLine = true
		}
	}
	return hasCommitLine
}

// gitLogAccumulator walks git log lines, collecting one commit block at a time
// and emitting a single condensed line per commit on flush.
type gitLogAccumulator struct {
	emitted                   []string
	hash, decoration, subject string
	isMerge                   bool
	inBlock                   bool
}

// consume processes one git log line, advancing the current commit block.
func (a *gitLogAccumulator) consume(line string) {
	if m := reGitLogCommitLine.FindStringSubmatch(line); m != nil {
		a.flush()
		a.hash = m[1]
		a.decoration = m[2]
		a.subject = ""
		a.isMerge = false
		a.inBlock = true
		return
	}
	if !a.inBlock {
		return
	}
	if strings.HasPrefix(line, "Merge: ") {
		a.isMerge = true
		return
	}
	if isGitLogHeaderLine(line) || line == "" {
		return
	}
	// Indented body lines (4 spaces): first one is the subject, rest skipped.
	if strings.HasPrefix(line, "    ") {
		if a.subject == "" {
			a.subject = strings.TrimSpace(line)
		}
		return
	}
	// Any other line: skip (defensive).
}

// flush emits the condensed line for the current commit block, if any.
func (a *gitLogAccumulator) flush() {
	if !a.inBlock || a.hash == "" {
		return
	}
	var sb strings.Builder
	sb.WriteString(a.hash[:min(len(a.hash), 8)])
	if a.decoration != "" {
		sb.WriteString(" (")
		sb.WriteString(a.decoration)
		sb.WriteByte(')')
	}
	if a.isMerge {
		sb.WriteString(" [merge]")
	}
	if a.subject != "" {
		sb.WriteByte(' ')
		sb.WriteString(a.subject)
	}
	a.emitted = append(a.emitted, sb.String())
}

// isGitLogHeaderLine reports whether a line is a git log metadata header
// (Author/Commit/Date variants) that is dropped from condensed output.
func isGitLogHeaderLine(line string) bool {
	return strings.HasPrefix(line, "Author:") ||
		strings.HasPrefix(line, "AuthorDate:") ||
		strings.HasPrefix(line, "Commit:") ||
		strings.HasPrefix(line, "CommitDate:") ||
		strings.HasPrefix(line, "Date:")
}
