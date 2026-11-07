package jinn

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The streaming read must not pay for a digest the caller did not ask for:
// Checksum stays empty, but Content/TotalLines are unaffected. When the
// digest is requested (flag or if_checksum), it must be the real SHA-256.
func TestReadFileContentChecksumOnlyWhenRequested(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	content := "line one\nline two\nline three\n"
	writeTestFile(t, dir, "a.txt", content)
	path := filepath.Join(dir, "a.txt")
	wantSum := sha256.Sum256([]byte(content))
	wantHex := hex.EncodeToString(wantSum[:])

	res, err := e.readFileContent(path, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Checksum != "" {
		t.Errorf("unrequested checksum = %q, want empty", res.Checksum)
	}
	if res.TotalLines != 3 {
		t.Errorf("TotalLines = %d, want 3", res.TotalLines)
	}
	if !strings.Contains(res.Content, "line one") {
		t.Errorf("Content missing first line: %q", res.Content)
	}

	res2, err := e.readFileContent(path, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Checksum != wantHex {
		t.Errorf("requested checksum = %q, want %q", res2.Checksum, wantHex)
	}

	// include_checksum in args also triggers the digest even when the
	// caller flag is false.
	res3, err := e.readFileContent(path, map[string]interface{}{"include_checksum": true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res3.Checksum != wantHex {
		t.Errorf("arg-requested checksum = %q, want %q", res3.Checksum, wantHex)
	}
}

func TestCountLinesUpTo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, s string
		limit   int
		want    int
	}{
		{"empty", "", 5, 0},
		{"single no newline", "a", 5, 1},
		{"trailing newline not extra", "a\nb\n", 5, 2},
		{"no trailing newline", "a\nb", 5, 2},
		{"blank lines count", "a\n\nb\n", 5, 3},
		{"stops at limit", "a\nb\nc\nd\ne\nf", 3, 3},
		{"limit 1", "a\nb", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := countLinesUpTo(tc.s, tc.limit)
			if got != tc.want {
				t.Errorf("countLinesUpTo(%q, %d) = %d, want %d", tc.s, tc.limit, got, tc.want)
			}
			// Must agree with splitLines length when under the limit.
			if tc.want < tc.limit && got != len(splitLines(tc.s)) {
				t.Errorf("countLinesUpTo(%q) = %d, splitLines len = %d", tc.s, got, len(splitLines(tc.s)))
			}
		})
	}
}

func TestShouldPruneTraversalSegments(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{"a/b/c.txt", false, false},
		{".hidden/e.txt", false, true},
		{"a/.hidden", false, true},
		{"vendor/pkg/x.go", true, true},
		{"vendor", false, false}, // excluded names prune dirs, not files
		{"node_modules", true, true},
		{"sub/vendor/deep", false, false}, // file under excluded dir: dir prune handles it at walk time
		{"a/b", true, false},
	} {
		if got := shouldPruneTraversal(tc.rel, tc.isDir); got != tc.want {
			t.Errorf("shouldPruneTraversal(%q, %v) = %v, want %v", tc.rel, tc.isDir, got, tc.want)
		}
	}
}

func TestRootRelativeFast(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ root, path, want string }{
		{".", ".", "."},
		{".", "a.txt", "a.txt"},
		{".", "sub/c.txt", "sub/c.txt"},
		{"sub", "sub", "."},
		{"sub", "sub/c.txt", "c.txt"},
		{"sub", "other/x.txt", ""}, // miss -> caller falls back to filepath.Rel
	} {
		if got := rootRelativeFast(tc.root, tc.path); got != tc.want {
			t.Errorf("rootRelativeFast(%q, %q) = %q, want %q", tc.root, tc.path, got, tc.want)
		}
	}
}

func TestOutsideWorkdirErrExplainsTruncationAndTemp(t *testing.T) {
	t.Parallel()
	trunc := outsideWorkdirErr("3h/k2znk2mn75j6cchzz9mhflrh0000gn/…/x.txt")
	if !strings.Contains(trunc.Err.Error(), "truncated") {
		t.Errorf("truncated err = %q, want 'truncated' explanation", trunc.Err)
	}
	if !strings.Contains(trunc.Suggestion, "narrower output") {
		t.Errorf("truncated suggestion = %q, want recovery advice", trunc.Suggestion)
	}
	temp := outsideWorkdirErr(filepath.Join(os.TempDir(), "pi-codemode-abc.txt"))
	if !strings.Contains(temp.Err.Error(), "temp/spill") {
		t.Errorf("temp err = %q, want temp/spill explanation", temp.Err)
	}
	plain := outsideWorkdirErr("/etc/passwd")
	if !strings.Contains(plain.Err.Error(), "outside working directory") {
		t.Errorf("plain err = %q, want outside-working-directory", plain.Err)
	}
	for _, e := range []*ErrWithSuggestion{trunc, temp, plain} {
		if e.Code != ErrCodePathOutsideSandbox {
			t.Errorf("code = %q, want %q", e.Code, ErrCodePathOutsideSandbox)
		}
	}
}

// A relative truncated path resolves inside the workdir, so checkPath must
// reject it on truncation alone, not as a misleading file-not-found.
func TestCheckPathRejectsTruncatedRelativePath(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.checkPath("a/b…/x.txt")
	var sErr *ErrWithSuggestion
	if !errors.As(err, &sErr) {
		t.Fatalf("checkPath truncated err = %v, want ErrWithSuggestion", err)
	}
	if !strings.Contains(sErr.Err.Error(), "truncated") {
		t.Errorf("err = %q, want truncated explanation", sErr.Err)
	}
}

func TestParseRowRecordsSingleParseSearchText(t *testing.T) {
	t.Parallel()
	parsed, err := parseRowRecords([]byte("{\"msg\":\"hello world\",\"n\":42}\n{\"n\":7}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.total != 2 {
		t.Fatalf("total = %d, want 2", parsed.total)
	}
	if len(parsed.keys) != 2 {
		t.Errorf("keys = %v, want msg+n", parsed.keys)
	}
	if !strings.Contains(parsed.rows[0].searchText, "hello world") {
		t.Errorf("searchText[0] = %q, want string values", parsed.rows[0].searchText)
	}
	// No string values: fall back to the raw record, matching the
	// pre-single-parse rowStringValues behavior.
	if parsed.rows[1].searchText != `{"n":7}` {
		t.Errorf("searchText[1] = %q, want raw line fallback", parsed.rows[1].searchText)
	}
}
