package jinn

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseSearchResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want int // expected number of results
	}{
		{
			name: "basic match",
			raw:  "file.txt:10:hello world\n",
			want: 1,
		},
		{
			name: "multiple files",
			raw:  "a.go:5:func foo() {}\nb.go:12:func bar() {}\n",
			want: 2,
		},
		{
			name: "group separator",
			raw:  "a.go:5:match one\n--\nb.go:10:match two\n",
			want: 2,
		},
		{
			name: "context lines with dash separator",
			raw:  "a.go-4-before\na.go:5:MATCH\na.go-6-after\n",
			want: 1,
		},
		{
			name: "empty input",
			raw:  "",
			want: 0,
		},
		{
			name: "rg column format",
			raw:  "file.go:42:10:some text\n",
			want: 1,
		},
		{
			name: "no match returns empty slice not nil",
			raw:  "Binary file foo.bin matches\n",
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			results, _ := parseSearchResults(tc.raw, 0)
			if len(results) != tc.want {
				t.Errorf("expected %d results, got %d (raw=%q)", tc.want, len(results), tc.raw)
			}
			// Verify nil safety: empty results should be an empty slice, not nil
			if tc.want == 0 && results == nil {
				t.Error("expected non-nil empty slice")
			}
		})
	}
}

func TestParseSearchResults_ContextFields(t *testing.T) {
	t.Parallel()
	// Context lines use '-' separator, match lines use ':'
	raw := "f.txt-4-before line\nf.txt:5:MATCH\nf.txt-6-after line\n"
	results, _ := parseSearchResults(raw, 0)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Line != 5 {
		t.Errorf("expected line 5, got %d", r.Line)
	}
	if !strings.Contains(r.ContextBefore, "before line") {
		t.Errorf("expected 'before line' in context_before, got %q", r.ContextBefore)
	}
	if !strings.Contains(r.ContextAfter, "after line") {
		t.Errorf("expected 'after line' in context_after, got %q", r.ContextAfter)
	}
}

func TestParseSearchResults_ColumnField(t *testing.T) {
	t.Parallel()
	raw := "f.go:42:10:code here\n"
	results, _ := parseSearchResults(raw, 0)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Line != 42 {
		t.Errorf("expected line 42, got %d", r.Line)
	}
	if r.Column != 10 {
		t.Errorf("expected column 10, got %d", r.Column)
	}
	if r.Text != "code here" {
		t.Errorf("expected text 'code here', got %q", r.Text)
	}
}

func TestParseSearchResults_MatchText(t *testing.T) {
	t.Parallel()
	// Standard grep format: file:line:text (leading ':' stripped)
	raw := "file.go:10:some text here\n"
	results, _ := parseSearchResults(raw, 0)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Text != "some text here" {
		t.Errorf("expected text 'some text here', got %q", r.Text)
	}
}

// TestParseSearchResults_ContextOrder verifies that ContextBefore is in top-to-bottom
// (chronological) order when context_lines > 1. This is a regression test for a bug
// where the preContext buffer was iterated in reverse, producing inverted context.
func TestParseSearchResults_ContextOrder(t *testing.T) {
	t.Parallel()
	// Simulate grep -C 2 output: two context-before lines then the match.
	raw := "f.go-2-beta\nf.go-3-gamma\nf.go:4:TARGET\n"
	results, _ := parseSearchResults(raw, 0)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	cb := results[0].ContextBefore
	betaIdx := strings.Index(cb, "beta")
	gammaIdx := strings.Index(cb, "gamma")
	if betaIdx < 0 || gammaIdx < 0 {
		t.Fatalf("expected both 'beta' and 'gamma' in context_before, got: %q", cb)
	}
	if betaIdx > gammaIdx {
		t.Errorf("context_before order is reversed: 'beta' (idx %d) should appear before 'gamma' (idx %d), got: %q",
			betaIdx, gammaIdx, cb)
	}
}

func TestParseSearchResults_RespectsCap(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := range 100 {
		fmt.Fprintf(&b, "f.go:%d:match%d\n", i+1, i)
	}
	results, total := parseSearchResults(b.String(), 10)
	if len(results) != 10 {
		t.Errorf("expected 10 results (capped), got %d", len(results))
	}
	if total != 100 {
		t.Errorf("expected total=100 even when capped, got %d", total)
	}
}
