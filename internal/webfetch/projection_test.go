package webfetch

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestProjectContentBudgets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		limits  OutputLimits
		want    string
		by      string
	}{
		{name: "unlimited", content: "one\ntwo\n", want: "one\ntwo\n"},
		{name: "lines", content: "one\ntwo\nthree\n", limits: OutputLimits{MaxLines: 2}, want: "one\ntwo\n", by: "lines"},
		{name: "bytes after lines", content: "abc\ndef\n", limits: OutputLimits{MaxBytes: 3, MaxLines: 1}, want: "abc", by: "bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ProjectContent(tt.content, tt.limits)
			if got.Content != tt.want || got.TruncatedBy != tt.by {
				t.Fatalf("projection = %+v", got)
			}
			if got.Truncated != (tt.want != tt.content) {
				t.Fatalf("truncated = %v", got.Truncated)
			}
		})
	}
}

func TestProjectContentPreservesUTF8(t *testing.T) {
	t.Parallel()
	got := ProjectContent("😀😀\n", OutputLimits{MaxBytes: 5})
	if !got.Truncated || got.TruncatedBy != "bytes" || !utf8.ValidString(got.Content) || got.OutputBytes > 5 {
		t.Fatalf("projection = %+v", got)
	}
}

func TestProjectFetchContentFormatsAndContinuation(t *testing.T) {
	t.Parallel()
	headings, err := ProjectFetchContent("# First\nbody\n## Second\n", "headings", 0, OutputLimits{MaxLines: 1})
	if err != nil {
		t.Fatal(err)
	}
	if headings.Content != "# First\n" || headings.Format != "headings" || headings.NextStartLine != 1 || !headings.Truncated {
		t.Fatalf("headings = %+v", headings)
	}
	links, err := ProjectFetchContent("[one](https://example.com/one)\n<https://example.com/two>\n[again](https://example.com/one)\n", "links", 1, OutputLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if links.Content != "https://example.com/two\n" || links.TruncatedBy != "offset" || links.StartLine != 1 {
		t.Fatalf("links = %+v", links)
	}
}

func TestProjectionValidationReturnsCodedErrors(t *testing.T) {
	t.Parallel()
	for _, run := range []func() error{
		func() error { _, err := NewOutputLimits(-1, 0); return err },
		func() error { _, err := ProjectFetchContent("x", "unknown", 0, OutputLimits{}); return err },
		func() error { _, err := ProjectFetchContent("x", "markdown", -1, OutputLimits{}); return err },
	} {
		err := run()
		var coded *CodedError
		if !errors.As(err, &coded) || coded.Code != ErrorCodeInvalidArgument || strings.TrimSpace(coded.Suggestion) == "" {
			t.Fatalf("error = %#v", err)
		}
	}
}
