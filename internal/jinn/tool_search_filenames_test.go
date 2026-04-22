package jinn

import (
	"strings"
	"testing"
)

func TestSearchFiles_FilenamesFormat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		files     map[string]string
		pattern   string
		wantFiles []string
		wantCount int
	}{
		{
			name: "single file with matches",
			files: map[string]string{
				"a.go": "package main\nfunc hello() {}\nfunc hello() {}\nfunc hello() {}\n",
			},
			pattern:   "hello",
			wantFiles: []string{"a.go"},
			wantCount: 3,
		},
		{
			name: "multiple files, some with no matches",
			files: map[string]string{
				"main.go":  "package main\nfunc foo() {}\nfunc foo() {}\n",
				"util.go":  "package main\nfunc bar() {}\n",
				"empty.go": "package main\n",
			},
			pattern:   "func",
			wantFiles: []string{"main.go", "util.go"},
			wantCount: 3,
		},
		{
			name: "single match uses singular",
			files: map[string]string{
				"lone.go": "package main\nfunc unique() {}\n",
			},
			pattern:   "unique",
			wantFiles: []string{"lone.go"},
			wantCount: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, dir := testEngine(t)
			for name, content := range tc.files {
				writeTestFile(t, dir, name, content)
			}

			result, err := e.searchFiles(args("pattern", tc.pattern, "format", "filenames"))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.HasPrefix(strings.TrimSpace(result), "[") {
				t.Errorf("filenames format should not return JSON, got: %s", result)
			}
			for _, f := range tc.wantFiles {
				if !strings.Contains(result, f) {
					t.Errorf("expected %q in results, got: %s", f, result)
				}
			}
			if tc.wantCount == 1 {
				if !strings.Contains(result, "1 match\n") && !strings.HasSuffix(result, "1 match") {
					t.Errorf("expected singular '1 match' in results, got: %s", result)
				}
			} else {
				if !strings.Contains(result, "matches") {
					t.Errorf("expected 'matches' in results, got: %s", result)
				}
			}
		})
	}
}

func TestSearchFiles_FilenamesNoMatch(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "empty.go", "package main\n")

	result, err := e.searchFiles(args("pattern", "ZZZ_NO_SUCH_PATTERN_ZZZ", "format", "filenames"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty string for no match, got: %q", result)
	}
}

func TestSearchFiles_MaxResults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		maxResults float64
		format     string
		wantCapped bool
	}{
		{
			name:       "text format capped at 2",
			maxResults: 2,
			format:     "text",
			wantCapped: true,
		},
		{
			name:       "max_results 0 is unlimited",
			maxResults: 0,
			format:     "text",
			wantCapped: false,
		},
		{
			name:       "filenames format with max_results",
			maxResults: 2,
			format:     "filenames",
			wantCapped: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, dir := testEngine(t)
			writeTestFile(t, dir, "multi.go", "aaa\naaa\naaa\naaa\naaa\n")

			result, err := e.searchFiles(args("pattern", "aaa", "format", tc.format, "max_results", tc.maxResults))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			cappedNote := "results capped at max_results"
			if tc.wantCapped && !strings.Contains(result, cappedNote) {
				t.Errorf("expected cap note in output, got: %s", result)
			}
			if !tc.wantCapped && strings.Contains(result, cappedNote) {
				t.Errorf("unexpected cap note in output, got: %s", result)
			}
		})
	}
}

func TestParseFilenamesOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		raw        string
		maxResults int
		want       string
	}{
		{
			name:       "single file with count",
			raw:        "main.go:3\n",
			maxResults: 0,
			want:       "main.go: 3 matches",
		},
		{
			name:       "single match uses singular",
			raw:        "util.go:1\n",
			maxResults: 0,
			want:       "util.go: 1 match",
		},
		{
			name:       "multiple files",
			raw:        "main.go:3\nutil.go:1\n",
			maxResults: 0,
			want:       "main.go: 3 matches\nutil.go: 1 match",
		},
		{
			name:       "zero-count files excluded",
			raw:        "main.go:3\nempty.go:0\n",
			maxResults: 0,
			want:       "main.go: 3 matches",
		},
		{
			name:       "empty input",
			raw:        "",
			maxResults: 0,
			want:       "",
		},
		{
			name:       "capped note when total meets max",
			raw:        "a.go:5\nb.go:3\n",
			maxResults: 5,
			want:       "a.go: 5 matches\nb.go: 3 matches\n(results capped at max_results=5, more matches may exist)",
		},
		{
			name:       "no capped note when total under max",
			raw:        "a.go:2\n",
			maxResults: 10,
			want:       "a.go: 2 matches",
		},
		{
			name:       "malformed lines skipped",
			raw:        "main.go:3\ngarbage\n",
			maxResults: 0,
			want:       "main.go: 3 matches",
		},
		{
			name:       "path with colons uses last colon",
			raw:        "/some/path/file.txt:3\n",
			maxResults: 0,
			want:       "/some/path/file.txt: 3 matches",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := parseFilenamesOutput(tc.raw, tc.maxResults)
			if got != tc.want {
				t.Errorf("parseFilenamesOutput(%q, %d)\ngot:  %q\nwant: %q", tc.raw, tc.maxResults, got, tc.want)
			}
		})
	}
}
