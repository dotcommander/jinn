package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestDiffPreview_DisabledIsNoop(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	dp := &diffPreview{enabled: false, out: &buf}

	dp.Feed(`{"path":"x","old_text":"a","new_text":"b"}`)
	dp.Render()

	if buf.Len() != 0 {
		t.Errorf("expected no output when disabled, got %q", buf.String())
	}
}

func TestDiffPreview_ExtractsCompleteFields(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	dp := &diffPreview{enabled: true, out: &buf}

	dp.Feed(`{"path":"a.go","old_text":"foo","new_text":"bar"}`)
	dp.Render()

	out := buf.String()
	for _, want := range []string{"a.go", "foo", "bar"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestDiffPreview_PartialJSONGraceful(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	dp := &diffPreview{enabled: true, out: &buf}

	// Partial feed — must not panic.
	dp.Feed(`{"path":"a.go","old_text":"x`)
	dp.Render() // may render partial or nothing — either is acceptable

	// Feed the rest.
	dp.Feed(`yz","new_text":"ab"}`)
	// Force render by resetting throttle.
	dp.shown = false
	dp.Render()

	out := buf.String()
	if !strings.Contains(out, "a.go") {
		t.Errorf("expected output to contain %q after complete feed, got:\n%s", "a.go", out)
	}
}

func TestDiffPreview_ResetClearsState(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	dp := &diffPreview{enabled: true, out: &buf}

	dp.Feed(`{"path":"old.go","old_text":"x","new_text":"y"}`)
	dp.Render()
	dp.Reset()

	// After reset, a fresh Render should produce no output (no fields).
	buf.Reset()
	dp.Render()
	if buf.Len() != 0 {
		t.Errorf("expected no output after Reset, got %q", buf.String())
	}
}

func TestDiffPreview_TruncatesLongDiff(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	dp := &diffPreview{enabled: true, out: &buf}

	// Build an old_text with 15 lines and new_text with 10 lines → total >20,
	// so the truncation path must be hit.
	old := strings.Repeat("line\n", 15)
	neu := strings.Repeat("line\n", 10)
	// Construct JSON manually since the content has newlines that need escaping.
	payload := `{"path":"big.go","old_text":"` +
		strings.ReplaceAll(strings.TrimRight(old, "\n"), "\n", `\n`) +
		`","new_text":"` +
		strings.ReplaceAll(strings.TrimRight(neu, "\n"), "\n", `\n`) +
		`"}`
	dp.Feed(payload)
	dp.Render()

	out := buf.String()
	if !strings.Contains(out, "truncated") {
		t.Errorf("expected truncation marker in output, got:\n%s", out)
	}
}

// TestDiffPreview_200msThrottle verifies the throttle constant is 200ms by
// confirming that a second Render within the throttle window produces no
// additional output, and a render after the window does.
func TestDiffPreview_200msThrottle(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	dp := &diffPreview{enabled: true, out: &buf}
	dp.Feed(`{"path":"t.go","old_text":"x","new_text":"y"}`)

	// First render — should produce output.
	dp.Render()
	after1 := buf.Len()
	if after1 == 0 {
		t.Fatal("expected output on first Render, got none")
	}

	// Immediate second render — throttled, no new output.
	dp.Render()
	if buf.Len() != after1 {
		t.Errorf("expected no output from throttled Render, got additional %d bytes", buf.Len()-after1)
	}

	// Force past the 200ms window by backdating lastRend.
	dp.lastRend = dp.lastRend.Add(-201 * time.Millisecond)
	dp.Render()
	if buf.Len() == after1 {
		t.Errorf("expected new output after throttle window elapsed, got none")
	}
}

func TestCRLFWriter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   string
		wantOut string
		wantN   int
	}{
		{"empty", "", "", 0},
		{"no newline", "hello", "hello", 5},
		{"bare LF", "hello\n", "hello\r\n", 6},
		{"already CRLF", "hello\r\n", "hello\r\n", 7},
		{"multi bare LF", "a\nb\nc", "a\r\nb\r\nc", 5},
		{"mixed CRLF and LF", "\r\n\n", "\r\n\r\n", 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			w := newCRLFWriter(&buf)
			n, err := w.Write([]byte(tc.input))
			if err != nil {
				t.Fatalf("Write error: %v", err)
			}
			if n != tc.wantN {
				t.Errorf("n = %d, want %d", n, tc.wantN)
			}
			if got := buf.String(); got != tc.wantOut {
				t.Errorf("output = %q, want %q", got, tc.wantOut)
			}
		})
	}
}

// TestDiffPreview_AppendsWhenNotTTY verifies that a bytes.Buffer destination
// (non-TTY) gets plain append output with no ANSI escape sequences for
// cursor movement. Each Render must add content, not erase previous content.
func TestDiffPreview_AppendsWhenNotTTY(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	dp := &diffPreview{enabled: true, out: &buf}
	dp.Feed(`{"path":"a.go","old_text":"old","new_text":"new"}`)

	dp.Render()
	after1 := buf.String()
	if after1 == "" {
		t.Fatal("expected output on first Render")
	}

	// Simulate a second render by resetting throttle; buffer must grow (append),
	// not shrink (erase-and-rewrite is TTY-only).
	dp.lastRend = dp.lastRend.Add(-201 * time.Millisecond)
	dp.Render()
	after2 := buf.String()

	if len(after2) <= len(after1) {
		t.Errorf("expected buffer to grow on second render (append mode), got len %d → %d", len(after1), len(after2))
	}
	// No cursor-up escape sequences must appear in non-TTY output.
	if strings.Contains(after2, "\x1b[1A") {
		t.Errorf("cursor-up escape sequence leaked into non-TTY output")
	}
	if strings.Contains(after2, "\x1b[2K") {
		t.Errorf("clear-line escape sequence leaked into non-TTY output")
	}
}

func TestCRLFWriter_SplitCRLFAcrossWrites(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := newCRLFWriter(&buf)
	n1, err1 := w.Write([]byte("hello\r"))
	n2, err2 := w.Write([]byte("\nworld"))
	if err1 != nil {
		t.Fatalf("first Write error: %v", err1)
	}
	if err2 != nil {
		t.Fatalf("second Write error: %v", err2)
	}
	if n1 != 6 {
		t.Errorf("n1 = %d, want 6", n1)
	}
	if n2 != 6 {
		t.Errorf("n2 = %d, want 6", n2)
	}
	// \r\n pair split across writes must not produce \r\r\n
	if got := buf.String(); got != "hello\r\nworld" {
		t.Errorf("output = %q, want %q", got, "hello\r\nworld")
	}
}
