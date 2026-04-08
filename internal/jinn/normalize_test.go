package jinn

import (
	"strings"
	"testing"
)

// --- stripBom ---

func TestStripBom_NoBom(t *testing.T) {
	t.Parallel()
	content, bom := stripBom("hello world")
	if bom != "" || content != "hello world" {
		t.Errorf("no BOM: content=%q bom=%q", content, bom)
	}
}

func TestStripBom_WithBom(t *testing.T) {
	t.Parallel()
	content, bom := stripBom("\xEF\xBB\xBFhello world")
	if bom != "\xEF\xBB\xBF" || content != "hello world" {
		t.Errorf("with BOM: content=%q bom=%q", content, bom)
	}
}

// --- detectLineEnding ---

func TestDetectLineEnding(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, input, want string
	}{
		{"lf", "hello\nworld\n", "\n"},
		{"crlf", "hello\r\nworld\r\n", "\r\n"},
		{"mixed_lf_first", "hello\nworld\r\n", "\n"},
		{"mixed_crlf_first", "hello\r\nworld\n", "\r\n"},
		{"no_newlines", "hello", "\n"},
		{"empty", "", "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := detectLineEnding(tc.input); got != tc.want {
				t.Errorf("detectLineEnding(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// --- normalizeToLF / restoreLineEndings ---

func TestNormalizeAndRestore(t *testing.T) {
	t.Parallel()
	input := "line1\r\nline2\r\nline3\r\n"
	normalized := normalizeToLF(input)
	if strings.Contains(normalized, "\r") {
		t.Fatal("normalizeToLF should remove all \\r")
	}
	restored := restoreLineEndings(normalized, "\r\n")
	if restored != input {
		t.Errorf("round-trip failed: got %q, want %q", restored, input)
	}
}

func TestNormalizeToLF_BareCarriageReturn(t *testing.T) {
	t.Parallel()
	input := "hello\rworld"
	got := normalizeToLF(input)
	if got != "hello\nworld" {
		t.Errorf("bare CR: got %q, want %q", got, "hello\nworld")
	}
}

// --- normalizeForFuzzyMatch ---

func TestNormalizeForFuzzyMatch_SmartQuotes(t *testing.T) {
	t.Parallel()
	input := "\u201CHello\u201D said \u2018world\u2019"
	got := normalizeForFuzzyMatch(input)
	want := `"Hello" said 'world'`
	if got != want {
		t.Errorf("smart quotes: got %q, want %q", got, want)
	}
}

func TestNormalizeForFuzzyMatch_Dashes(t *testing.T) {
	t.Parallel()
	input := "one\u2013two\u2014three\u2212four"
	got := normalizeForFuzzyMatch(input)
	if got != "one-two-three-four" {
		t.Errorf("dashes: got %q, want %q", got, "one-two-three-four")
	}
}

func TestNormalizeForFuzzyMatch_TrailingWhitespace(t *testing.T) {
	t.Parallel()
	input := "hello   \nworld\t\n"
	got := normalizeForFuzzyMatch(input)
	if got != "hello\nworld\n" {
		t.Errorf("trailing ws: got %q, want %q", got, "hello\nworld\n")
	}
}

func TestNormalizeForFuzzyMatch_UnicodeSpaces(t *testing.T) {
	t.Parallel()
	input := "hello\u00A0world\u3000end"
	got := normalizeForFuzzyMatch(input)
	if got != "hello world end" {
		t.Errorf("unicode spaces: got %q, want %q", got, "hello world end")
	}
}

// --- shellescape ---

func TestShellescape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"hello", "'hello'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := shellescape(tc.in); got != tc.want {
				t.Errorf("shellescape(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
