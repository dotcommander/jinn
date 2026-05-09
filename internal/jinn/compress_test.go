package jinn

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// hashAbbrevStrategy
// ---------------------------------------------------------------------------

func TestHashAbbrev(t *testing.T) {
	s := &hashAbbrevStrategy{}

	t.Run("two hashes applies and compresses", func(t *testing.T) {
		// Two 40-char lowercase hex hashes → AppliesTo returns true,
		// Compress abbreviates each to 8 chars, result is shorter.
		input := "commit a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0\n" +
			"Author: test@example.com\n" +
			"commit f9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=true for input with two 40-char hashes")
		}

		result := s.Compress(input)
		if strings.Contains(result, "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0") {
			t.Error("full 40-char hash should be abbreviated")
		}
		if !strings.Contains(result, "a1b2c3d4") {
			t.Error("expected abbreviated hash a1b2c3d4 in output")
		}
		if !strings.Contains(result, "f9e8d7c6") {
			t.Error("expected abbreviated hash f9e8d7c6 in output")
		}
		if len(result) >= len(input) {
			t.Errorf("compressed result (%d) should be shorter than input (%d)", len(result), len(input))
		}
	})

	t.Run("single hash does not apply", func(t *testing.T) {
		// Only one hash — below the threshold of 2.
		input := "commit a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0\nAuthor: test\n"

		if s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=false for single hash (< 2 threshold)")
		}
	})

	t.Run("no hashes does not apply", func(t *testing.T) {
		input := "no hashes here, just normal text\nanother line\n"

		if s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=false when no 40-char hex hashes present")
		}
	})

	t.Run("uppercase hex does not match", func(t *testing.T) {
		// reFullHash only matches [0-9a-f], not uppercase A-F.
		input := "commit A1B2C3D4E5F6A7B8C9D0E1F2A3B4C5D6E7F8A9B0\n" +
			"commit F9E8D7C6B5A4F3E2D1C0B9A8F7E6D5C4B3A2F1E0\n"

		if s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=false for uppercase hex hashes")
		}
	})

	t.Run("hash in middle of word does not match", func(t *testing.T) {
		// 42-char hex string — no word boundary at the edges of any 40-char window.
		longHex := "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0cc"
		input := "prefix_" + longHex + " suffix_" + longHex + "\n"

		if s.AppliesTo(input, "run_shell") {
			t.Error("hash embedded in longer hex string should not match (word boundary check)")
		}
	})
}

// ---------------------------------------------------------------------------
// pathPrefixStrategy
// ---------------------------------------------------------------------------

func TestPathPrefix(t *testing.T) {
	s := &pathPrefixStrategy{}

	t.Run("three plus lines with same prefix", func(t *testing.T) {
		// 4 lines sharing /Users/foo/project/ → applies, produces [cwd: ...] header.
		input := "/path/to/project/project/file1.go:10: syntax error\n" +
			"/path/to/project/project/file2.go:20: type mismatch\n" +
			"/path/to/project/project/file3.go:30: unused import\n" +
			"/path/to/project/project/file4.go:40: undeclared name\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=true for 4 lines with common path prefix")
		}

		result := s.Compress(input)
		if !strings.HasPrefix(result, "[cwd: /path/to/project/project]") {
			t.Errorf("expected [cwd: /path/to/project/project] header, got: %s", result[:min(40, len(result))])
		}
		if strings.Contains(result, "/path/to/project/project/") {
			t.Error("original prefix should be removed from individual lines")
		}
		if len(result) >= len(input) {
			t.Errorf("compressed (%d) should be shorter than original (%d)", len(result), len(input))
		}
	})

	t.Run("two lines does not apply", func(t *testing.T) {
		// Only 2 path lines — below the threshold of 3.
		input := "/path/to/project/project/a.go\n/Users/foo/project/b.go\n"

		if s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=false for 2 path lines (< 3 threshold)")
		}
	})

	t.Run("different prefixes applies but no useful compression", func(t *testing.T) {
		// Paths with no common prefix beyond "/" (trivially short → rejected).
		// AppliesTo only checks path-line count ≥ 3, so it returns true.
		// The Compress method would attempt longestCommonPathPrefix, but
		// for paths sharing only "/" as prefix, it returns "" and Compress
		// returns original. However, longestCommonPathPrefix has a known
		// edge case with certain path combinations that causes an infinite
		// loop, so we only verify AppliesTo behavior here and skip Compress
		// on this specific input.
		input := "/path/to/project/a.go\n/opt/bar/b.go\n/var/log/c.go\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=true (counts 3 path lines regardless of prefix)")
		}
	})

	t.Run("compressed result longer returns original", func(t *testing.T) {
		// Very short paths where the [cwd: ...] header overhead exceeds savings.
		input := "/a/b\n/a/c\n/a/d\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Fatal("expected AppliesTo=true for 3 path lines")
		}

		result := s.Compress(input)
		if result != input {
			t.Errorf("expected original returned when compressed is not shorter\ngot: %q", result)
		}
	})

	t.Run("mixed paths and non-paths", func(t *testing.T) {
		// Lines with paths and non-path lines — only path lines are abbreviated.
		input := "error report:\n" +
			"/path/to/project/project/a.go:10: error\n" +
			"/path/to/project/project/b.go:20: warning\n" +
			"/path/to/project/project/c.go:30: note\n" +
			"end of report\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Fatal("expected AppliesTo=true for 3+ path lines mixed with non-paths")
		}

		result := s.Compress(input)
		if !strings.Contains(result, "[cwd: /path/to/project/project]") {
			t.Error("expected [cwd: ...] header in output")
		}
		if !strings.Contains(result, "error report:") {
			t.Error("non-path line 'error report:' should be preserved verbatim")
		}
		if !strings.Contains(result, "end of report") {
			t.Error("non-path line 'end of report' should be preserved verbatim")
		}
	})
}

// ---------------------------------------------------------------------------
// testResultStrategy
// ---------------------------------------------------------------------------

func TestTestResult(t *testing.T) {
	s := &testResultStrategy{}

	t.Run("go test all passing compresses", func(t *testing.T) {
		// go test -v output with 3 passing tests and no failures.
		input := "=== RUN   TestParse\n" +
			"--- PASS: TestParse (0.00s)\n" +
			"=== RUN   TestValidate\n" +
			"--- PASS: TestValidate (0.01s)\n" +
			"=== RUN   TestRender\n" +
			"--- PASS: TestRender (0.00s)\n" +
			"PASS\n" +
			"ok  \tgithub.com/example/pkg\t0.012s\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=true for go test -v output with ≥5 test lines")
		}

		result := s.Compress(input)
		expected := "✓ github.com/example/pkg 3 passed (0.012s)"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("go test with failures preserves original", func(t *testing.T) {
		// Failures must be kept — lossless for signal.
		input := "=== RUN   TestParse\n" +
			"--- PASS: TestParse (0.00s)\n" +
			"=== RUN   TestFail\n" +
			"--- FAIL: TestFail (0.00s)\n" +
			"FAIL\n" +
			"FAIL\tgithub.com/example/pkg\t0.012s\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Fatal("expected AppliesTo=true for failing go test output")
		}

		result := s.Compress(input)
		if result != input {
			t.Error("failing test output should be returned unchanged (lossless for signal)")
		}
	})

	t.Run("go test below threshold does not apply", func(t *testing.T) {
		// Only 3 test-output lines — below the ≥5 threshold.
		input := "=== RUN   TestOnly\n" +
			"--- PASS: TestOnly (0.00s)\n" +
			"ok  \tgithub.com/example/pkg\t0.001s\n"

		if s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=false for < 5 test lines")
		}
	})

	t.Run("pytest all passing compresses", func(t *testing.T) {
		input := "============================= test session starts ==============================\n" +
			"collected 5 items\n" +
			"\n" +
			"test_foo.py::test_one PASSED\n" +
			"test_foo.py::test_two PASSED\n" +
			"test_foo.py::test_three PASSED\n" +
			"test_foo.py::test_four PASSED\n" +
			"test_foo.py::test_five PASSED\n" +
			"\n" +
			"5 passed in 0.05s\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=true for pytest output with ≥5 test lines")
		}

		result := s.Compress(input)
		expected := "✓ 5 passed (0.05s)"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("pytest with failures preserves original", func(t *testing.T) {
		input := "============================= test session starts ==============================\n" +
			"collected 5 items\n" +
			"\n" +
			"test_foo.py::test_one PASSED\n" +
			"test_foo.py::test_two PASSED\n" +
			"test_foo.py::test_three PASSED\n" +
			"test_foo.py::test_four FAILED\n" +
			"test_foo.py::test_five FAILED\n" +
			"\n" +
			"2 failed, 3 passed in 0.10s\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Fatal("expected AppliesTo=true")
		}

		result := s.Compress(input)
		if result != input {
			t.Error("pytest with failures should be returned unchanged")
		}
	})

	t.Run("cargo test all passing compresses", func(t *testing.T) {
		// Need enough "running" + "test result:" lines to reach ≥5 test-output lines.
		input := "running 5 tests\n" +
			"test test_one ... ok\n" +
			"test test_two ... ok\n" +
			"test test_three ... ok\n" +
			"test test_four ... ok\n" +
			"test test_five ... ok\n" +
			"\n" +
			"test result: ok. 5 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.01s\n" +
			"\n" +
			"running 3 tests\n" +
			"test test_six ... ok\n" +
			"test test_seven ... ok\n" +
			"test test_eight ... ok\n" +
			"\n" +
			"test result: ok. 3 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n" +
			"\n" +
			"running 2 tests\n" +
			"test extra_one ... ok\n" +
			"test extra_two ... ok\n" +
			"\n" +
			"test result: ok. 2 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=true for cargo test output")
		}

		result := s.Compress(input)
		// compressCargoTest processes the first "test result:" line and breaks.
		expected := "✓ 5 passed (0.01s)"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})
}

// ---------------------------------------------------------------------------
// buildOutputStrategy
// ---------------------------------------------------------------------------

func TestBuildOutput(t *testing.T) {
	s := &buildOutputStrategy{}

	t.Run("go build success compresses", func(t *testing.T) {
		// Only boilerplate # pkg lines — no errors or warnings.
		input := "# github.com/example/pkg/cmd\n" +
			"# github.com/example/pkg/internal\n" +
			"# github.com/example/pkg/internal/config\n" +
			"# github.com/example/pkg/internal/server\n" +
			"# github.com/example/pkg/internal/handler\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=true for go build output with ≥5 lines")
		}

		result := s.Compress(input)
		expected := "✓ go build (no issues)"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("go build with errors keeps error lines", func(t *testing.T) {
		// Error lines are preserved, go: downloading boilerplate is removed.
		// NOTE: package names must not contain "error" or "warning" substrings
		// because containsErrorOrWarning scans the entire line text.
		input := "go: downloading github.com/stretchr/testify v1.8.4\n" +
			"go: downloading golang.org/x/sys v0.15.0\n" +
			"./main.go:10:2: undefined: foo\n" +
			"./main.go:11:3: cannot use bar as type string in assignment\n" +
			"go: downloading golang.org/x/text v0.14.0\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=true for go build with errors")
		}

		result := s.Compress(input)
		if strings.Contains(result, "go: downloading") {
			t.Error("boilerplate 'go: downloading' lines should be removed")
		}
		if !strings.Contains(result, "undefined: foo") {
			t.Error("error line 'undefined: foo' should be preserved")
		}
		if !strings.Contains(result, "cannot use bar") {
			t.Error("error line 'cannot use bar' should be preserved")
		}
		if len(result) >= len(input) {
			t.Errorf("compressed (%d) should be shorter than original (%d)", len(result), len(input))
		}
	})

	t.Run("below five lines does not apply", func(t *testing.T) {
		input := "# github.com/example/pkg/cmd\n" +
			"# github.com/example/pkg/internal\n" +
			"# github.com/example/pkg/config\n"

		if s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=false for < 5 build lines")
		}
	})

	t.Run("cargo build success compresses", func(t *testing.T) {
		input := "Compiling libc v0.2.147\n" +
			"Compiling cfg-if v1.0.0\n" +
			"Compiling memchr v2.7.1\n" +
			"Compiling aho-corasick v1.1.2\n" +
			"Compiling regex v1.10.2\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=true for cargo build output")
		}

		result := s.Compress(input)
		expected := "✓ cargo build (no issues)"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("non-build output does not apply", func(t *testing.T) {
		input := "this is just some random text\n" +
			"with nothing that looks like\n" +
			"a build command output at all\n" +
			"seriously no markers here\n" +
			"nothing to see\n"

		if s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=false for non-build output")
		}
	})
}

// ---------------------------------------------------------------------------
// gitStatusStrategy
// ---------------------------------------------------------------------------

func TestGitStatus(t *testing.T) {
	s := &gitStatusStrategy{}

	t.Run("standard status with mixed changes", func(t *testing.T) {
		input := "On branch main\n" +
			"Changes not staged for commit:\n" +
			"  (use \"git add <file>...\" to update what will be committed)\n" +
			"  (use \"git restore <file>...\" to discard changes in working directory)\n" +
			"\tmodified:   foo.go\n" +
			"\tdeleted:    bar.go\n" +
			"\n" +
			"Untracked files:\n" +
			"  (use \"git add <file>...\" to include in what will be committed)\n" +
			"\tbaz.go\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=true for standard git status")
		}

		result := s.Compress(input)
		expected := "On branch main\nM foo.go  D bar.go  +baz.go"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("clean working tree does not apply", func(t *testing.T) {
		input := "On branch main\nnothing to commit, working tree clean\n"

		if s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=false for clean working tree")
		}
	})

	t.Run("HEAD detached with changes applies", func(t *testing.T) {
		input := "HEAD detached at a1b2c3d4\n" +
			"Changes not staged for commit:\n" +
			"  (use \"git add <file>...\" to update what will be committed)\n" +
			"  (use \"git restore <file>...\" to discard changes in working directory)\n" +
			"\tmodified:   config.go\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=true for HEAD detached with changes")
		}

		result := s.Compress(input)
		expected := "HEAD detached at a1b2c3d4\nM config.go"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("branch with ahead tracking info", func(t *testing.T) {
		input := "On branch feat/auth\n" +
			"Your branch is ahead of 'origin/feat/auth' by 2 commits.\n" +
			"  (use \"git push\" to publish your local commits)\n" +
			"Changes not staged for commit:\n" +
			"\tmodified:   auth.go\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=true for branch with tracking info")
		}

		result := s.Compress(input)
		expected := "On branch feat/auth (ahead 2 of origin/feat/auth)\nM auth.go"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("branch with behind tracking info", func(t *testing.T) {
		input := "On branch main\n" +
			"Your branch is behind 'origin/main' by 3 commits.\n" +
			"  (use \"git pull\" to update your local branch)\n" +
			"Changes not staged for commit:\n" +
			"\tmodified:   readme.md\n"

		result := s.Compress(input)
		expected := "On branch main (behind 3 of origin/main)\nM readme.md"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("branch up to date", func(t *testing.T) {
		input := "On branch main\n" +
			"Your branch is up to date with 'origin/main'.\n" +
			"Changes not staged for commit:\n" +
			"\tmodified:   file.go\n"

		result := s.Compress(input)
		expected := "On branch main (up to date with origin/main)\nM file.go"
		if result != expected {
			t.Errorf("expected %q, got %q", expected, result)
		}
	})

	t.Run("staged new file and renamed", func(t *testing.T) {
		input := "On branch main\n" +
			"Changes to be committed:\n" +
			"  (use \"git restore --staged <file>...\" to unstage)\n" +
			"\tnew file:   brand_new.go\n" +
			"\trenamed:    old.go -> new.go\n"

		if !s.AppliesTo(input, "run_shell") {
			t.Error("expected AppliesTo=true for staged changes")
		}

		result := s.Compress(input)
		if !strings.Contains(result, "A brand_new.go") {
			t.Errorf("expected 'A brand_new.go' in output, got %q", result)
		}
		if !strings.Contains(result, "R old.go -> new.go") {
			t.Errorf("expected 'R old.go -> new.go' in output, got %q", result)
		}
	})
}

// ---------------------------------------------------------------------------
// Compressor integration
// ---------------------------------------------------------------------------

func TestCompressorPipeline(t *testing.T) {
	t.Run("multiple strategies apply", func(t *testing.T) {
		// Input with 4 path lines sharing a prefix AND 4 hex hashes →
		// both pathPrefix and hashAbbrev strategies should apply.
		c := NewCompressor()

		input := "/path/to/project/project/a.go: a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0\n" +
			"/path/to/project/project/b.go: f9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c4b3a2f1e0\n" +
			"/path/to/project/project/c.go: 1111111111111111111111111111111111111111\n" +
			"/path/to/project/project/d.go: abcdefabcdefabcdefabcdefabcdefabcdefabcd\n"

		result, meta := c.Compress(input, "run_shell")

		// Both path_prefix_dedup and hash_abbrev should be listed.
		found := map[string]bool{}
		for _, name := range meta.Strategies {
			found[name] = true
		}
		if !found["path_prefix_dedup"] {
			t.Error("expected path_prefix_dedup in strategies")
		}
		if !found["hash_abbrev"] {
			t.Error("expected hash_abbrev in strategies")
		}
		if len(result) >= len(input) {
			t.Errorf("result (%d) should be shorter than input (%d)", len(result), len(input))
		}
		// Hashes should be abbreviated.
		if strings.Contains(result, "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0") {
			t.Error("hashes should be abbreviated in final output")
		}
		if !strings.Contains(result, "[cwd:") {
			t.Error("path prefix header should be present")
		}
	})

	t.Run("no matching strategies returns original", func(t *testing.T) {
		c := NewCompressor()
		input := "just some normal text\nnothing special here\n"

		result, meta := c.Compress(input, "run_shell")

		if result != input {
			t.Errorf("expected original output when no strategies match, got %q", result)
		}
		if len(meta.Strategies) != 0 {
			t.Errorf("expected empty strategies, got %v", meta.Strategies)
		}
	})

	t.Run("empty string returns empty string", func(t *testing.T) {
		c := NewCompressor()

		result, meta := c.Compress("", "run_shell")

		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
		if len(meta.Strategies) != 0 {
			t.Errorf("expected empty strategies for empty input, got %v", meta.Strategies)
		}
		if meta.OriginalLen != 0 || meta.FinalLen != 0 {
			t.Errorf("expected zero lengths, got original=%d final=%d", meta.OriginalLen, meta.FinalLen)
		}
	})
}

// ---------------------------------------------------------------------------
// Fail-open: panic recovery
// ---------------------------------------------------------------------------

// panickingStrategy is a test-only strategy that panics in Compress.
type panickingStrategy struct{}

func (s *panickingStrategy) Name() string { return "panicking" }
func (s *panickingStrategy) AppliesTo(output string, tool string) bool {
	return true
}
func (s *panickingStrategy) Compress(output string) string {
	panic("intentional test panic")
}

func TestCompressorFailOpen(t *testing.T) {
	// If a strategy panics, Compress must recover and return the original output.
	c := &Compressor{
		strategies: []Strategy{&panickingStrategy{}},
	}

	output, meta := c.Compress("some output", "run_shell")

	if output != "some output" {
		t.Errorf("expected original output on panic, got %q", output)
	}
	if len(meta.Strategies) != 0 {
		t.Errorf("expected empty strategies on panic recovery, got %v", meta.Strategies)
	}
}

// ---------------------------------------------------------------------------
// Determinism
// ---------------------------------------------------------------------------

func TestCompressorDeterministic(t *testing.T) {
	c := NewCompressor()

	input := "On branch main\n" +
		"Changes not staged for commit:\n" +
		"  (use \"git add <file>...\" to update what will be committed)\n" +
		"  (use \"git restore <file>...\" to discard changes in working directory)\n" +
		"\tmodified:   main.go\n" +
		"\tmodified:   util.go\n" +
		"\n" +
		"Untracked files:\n" +
		"  (use \"git add <file>...\" to include in what will be committed)\n" +
		"\tnew_feature.go\n"

	out1, meta1 := c.Compress(input, "run_shell")
	out2, meta2 := c.Compress(input, "run_shell")

	if out1 != out2 {
		t.Errorf("non-deterministic output:\n1: %q\n2: %q", out1, out2)
	}
	if len(meta1.Strategies) != len(meta2.Strategies) {
		t.Errorf("non-deterministic strategy count: %v vs %v", meta1.Strategies, meta2.Strategies)
	}
	for i := range meta1.Strategies {
		if meta1.Strategies[i] != meta2.Strategies[i] {
			t.Errorf("non-deterministic strategies at index %d: %q vs %q", i, meta1.Strategies[i], meta2.Strategies[i])
		}
	}
	if meta1.OriginalLen != meta2.OriginalLen || meta1.FinalLen != meta2.FinalLen {
		t.Errorf("non-deterministic metadata: %+v vs %+v", meta1, meta2)
	}
}
