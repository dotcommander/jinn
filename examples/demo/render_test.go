package main

import (
	"bytes"
	"strings"
	"testing"
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
