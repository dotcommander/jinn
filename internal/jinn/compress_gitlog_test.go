package jinn

import (
	"regexp"
	"strings"
	"testing"
)

// fakeGitLog builds a synthetic medium-format git log block.
// hash must be a 40-char hex string.
func fakeGitLogBlock(hash, decoration, subject string) string {
	var sb strings.Builder
	if decoration != "" {
		sb.WriteString("commit " + hash + " (" + decoration + ")\n")
	} else {
		sb.WriteString("commit " + hash + "\n")
	}
	sb.WriteString("Author: A User <a@example.com>\n")
	sb.WriteString("Date:   Mon May 10 00:00:00 2026 +0000\n")
	sb.WriteString("\n")
	sb.WriteString("    " + subject + "\n")
	sb.WriteString("\n")
	return sb.String()
}

const (
	hash1 = "1111111111111111111111111111111111111111"
	hash2 = "2222222222222222222222222222222222222222"
	hash3 = "3333333333333333333333333333333333333333"
)

func TestCondenseGitLog_Basic(t *testing.T) {
	t.Parallel()

	input := fakeGitLogBlock(hash1, "", "first subject line") +
		fakeGitLogBlock(hash2, "", "second subject line")

	result := condenseGitLog(input)

	lines := strings.Split(strings.TrimRight(result, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), result)
	}

	reHashPrefix := regexp.MustCompile(`^[0-9a-f]{8} `)
	for i, line := range lines {
		if !reHashPrefix.MatchString(line) {
			t.Errorf("line %d does not start with 8-hex prefix: %q", i, line)
		}
	}

	if !strings.Contains(lines[0], "first subject line") {
		t.Errorf("line 0 missing subject: %q", lines[0])
	}
	if !strings.Contains(lines[1], "second subject line") {
		t.Errorf("line 1 missing subject: %q", lines[1])
	}
	if strings.Contains(result, "Author:") {
		t.Errorf("result must not contain 'Author:': %q", result)
	}
	if strings.Contains(result, "Date:") {
		t.Errorf("result must not contain 'Date:': %q", result)
	}
}

func TestCondenseGitLog_Decoration(t *testing.T) {
	t.Parallel()

	input := fakeGitLogBlock(hash1, "HEAD -> main, tag: v2.0", "tagged release")

	result := condenseGitLog(input)

	if !strings.Contains(result, "(HEAD -> main, tag: v2.0)") {
		t.Errorf("expected decoration in result: %q", result)
	}
	// Decoration must appear after hash and before subject.
	hashPrefix := hash1[:8]
	hashIdx := strings.Index(result, hashPrefix)
	decoIdx := strings.Index(result, "(HEAD -> main, tag: v2.0)")
	subjectIdx := strings.Index(result, "tagged release")
	if hashIdx < 0 || decoIdx < 0 || subjectIdx < 0 {
		t.Fatalf("missing expected segments in: %q", result)
	}
	if !(hashIdx < decoIdx && decoIdx < subjectIdx) {
		t.Errorf("order wrong: hash=%d deco=%d subject=%d in %q", hashIdx, decoIdx, subjectIdx, result)
	}
}

func TestCondenseGitLog_Merge(t *testing.T) {
	t.Parallel()

	// A merge commit has a "Merge: aaa bbb" line.
	input := "commit " + hash1 + "\n" +
		"Merge: aaaaaaa bbbbbbb\n" +
		"Author: A User <a@example.com>\n" +
		"Date:   Mon May 10 00:00:00 2026 +0000\n" +
		"\n" +
		"    Merge branch 'feature' into main\n" +
		"\n"

	result := condenseGitLog(input)

	if !strings.Contains(result, "[merge]") {
		t.Errorf("expected [merge] tag in result: %q", result)
	}
	if strings.Contains(result, "Merge: aaaaaaa bbbbbbb") {
		t.Errorf("result must not contain raw Merge: line: %q", result)
	}
	if !strings.Contains(result, "Merge branch 'feature' into main") {
		t.Errorf("expected subject in result: %q", result)
	}
}

func TestCondenseGitLog_BailsOnDiff(t *testing.T) {
	t.Parallel()

	input := fakeGitLogBlock(hash1, "", "some commit") +
		"diff --git a/x b/x\n" +
		"index abc..def 100644\n" +
		"--- a/x\n" +
		"+++ b/x\n" +
		"@@ -1 +1 @@\n" +
		"-old\n" +
		"+new\n"

	result := condenseGitLog(input)
	if result != input {
		t.Errorf("expected unchanged output when diff present\ngot: %q\nwant: %q", result, input)
	}
}

func TestCondenseGitLog_BailsOnDiffstat(t *testing.T) {
	t.Parallel()

	input := fakeGitLogBlock(hash1, "", "some commit") +
		" 3 files changed, 10 insertions(+), 2 deletions(-)\n"

	result := condenseGitLog(input)
	if result != input {
		t.Errorf("expected unchanged output when diffstat present\ngot: %q\nwant: %q", result, input)
	}
}

func TestCondenseGitLog_BailsOnNonLog(t *testing.T) {
	t.Parallel()

	input := "fatal: your current branch 'main' does not have any commits yet\n"

	result := condenseGitLog(input)
	if result != input {
		t.Errorf("expected unchanged output for non-log input\ngot: %q\nwant: %q", result, input)
	}
}

func TestCompressShellOutput_GitLogGate(t *testing.T) {
	t.Parallel()

	s := fakeGitLogBlock(hash1, "HEAD -> main", "first commit") +
		fakeGitLogBlock(hash2, "", "second commit")

	// git log: should be condensed.
	resultGit := compressShellOutput(s, "git log")
	if len(resultGit) >= len(s) {
		t.Errorf("expected shorter result for 'git log', got len=%d vs input len=%d", len(resultGit), len(s))
	}
	reHashPrefix := regexp.MustCompile(`^[0-9a-f]{8} `)
	firstLine := strings.SplitN(resultGit, "\n", 2)[0]
	if !reHashPrefix.MatchString(firstLine) {
		t.Errorf("expected 8-hex-prefixed first line, got: %q", firstLine)
	}
	if strings.Contains(resultGit, "Author:") {
		t.Errorf("'git log' result must not contain 'Author:': %q", resultGit)
	}

	// echo hi: git-log gate must NOT fire, Author: must still be present.
	resultEcho := compressShellOutput(s, "echo hi")
	if !strings.Contains(resultEcho, "Author:") {
		t.Errorf("'echo hi' result must still contain 'Author:': %q", resultEcho)
	}
}
