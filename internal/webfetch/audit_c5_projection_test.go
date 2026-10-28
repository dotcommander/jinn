package webfetch

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAuditC503ReconstructsCappedContent(t *testing.T) {
	t.Parallel()
	for _, content := range []string{"abcdefghijk", "😀😀😀\nlast", "a\r\nb\r\n", "a\n", "", "prefix\n" + strings.Repeat("é", 30) + "\nend"} {
		var output strings.Builder
		line, offset := 0, 0
		for pages := 0; ; pages++ {
			if pages > len(content)+1 {
				t.Fatal("cursor stalled")
			}
			page, err := ProjectFetchContentAt(content, "markdown", line, offset, OutputLimits{MaxBytes: 5, MaxLines: 1})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Content) > 5 || !utf8.ValidString(page.Content) {
				t.Fatalf("invalid page: %+v", page)
			}
			output.WriteString(page.Content)
			if page.NextStartLine == 0 && page.NextStartLineByte == 0 {
				break
			}
			if line == page.NextStartLine && offset == page.NextStartLineByte {
				t.Fatalf("unchanged cursor: %+v", page)
			}
			line, offset = page.NextStartLine, page.NextStartLineByte
		}
		if output.String() != content {
			t.Fatalf("reconstructed %q, want %q", output.String(), content)
		}
	}
}

func TestAuditC503RejectsInvalidCursorAndInsufficientBudget(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ line, offset, cap int }{{0, -1, 0}, {0, 1, 0}, {0, 6, 0}, {0, 0, 1}, {1, 1, 0}} {
		if _, err := ProjectFetchContentAt("😀\n", "markdown", tc.line, tc.offset, OutputLimits{MaxBytes: tc.cap}); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	page, err := ProjectFetchContentAt("😀\n", "markdown", 0, 4, OutputLimits{MaxBytes: 1})
	if err != nil || page.Content != "\n" {
		t.Fatalf("terminator page = %+v, %v", page, err)
	}
	page, err = ProjectFetchContentAt("body\n# Heading\nother", "headings", 0, 3, OutputLimits{MaxBytes: 4})
	if err != nil || page.Content != "eadi" || page.NextStartLineByte != 7 {
		t.Fatalf("projected byte coordinates = %+v, %v", page, err)
	}
}

func TestAuditC5I01IterativeHeaders(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ input, title, headings string }{
		{"", "", ""},
		{"Title: Meta\r\n# Heading\r\n## Child", "Meta", "# Heading\n## Child\n"},
		{strings.Repeat("x", 1<<20) + "\n# Long input\n", "Long input", "# Long input\n"},
	} {
		if got := markdownTitle(tc.input); got != tc.title {
			t.Fatalf("title = %q, want %q", got, tc.title)
		}
		if got := extractHeadings(tc.input); got != tc.headings {
			t.Fatalf("headings = %q, want %q", got, tc.headings)
		}
	}
}
