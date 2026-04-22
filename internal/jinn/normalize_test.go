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
	input := "“Hello” said ‘world’"
	got := normalizeForFuzzyMatch(input)
	want := `"Hello" said 'world'`
	if got != want {
		t.Errorf("smart quotes: got %q, want %q", got, want)
	}
}

func TestNormalizeForFuzzyMatch_Dashes(t *testing.T) {
	t.Parallel()
	input := "one–two—three−four"
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
	input := "hello world　end"
	got := normalizeForFuzzyMatch(input)
	if got != "hello world end" {
		t.Errorf("unicode spaces: got %q, want %q", got, "hello world end")
	}
}

// --- closestLine ---

func TestClosestLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		oldText     string
		content     string
		wantLine    int
		wantContain string
	}{
		{
			name:        "exact match returns that line",
			oldText:     "func hello() {",
			content:     "package main\n\nfunc hello() {\n\treturn\n}\n",
			wantLine:    3,
			wantContain: "func hello() {",
		},
		{
			name:        "typo finds closest",
			oldText:     "func foo() err {",
			content:     "package main\n\nfunc foo() error {\n\treturn nil\n}\n",
			wantLine:    3,
			wantContain: "func foo() error {",
		},
		{
			name:        "partial match on long content",
			oldText:     "x := calculateTotal(",
			content:     strings.Repeat("line\n", 40) + "\tx := calculateTotal(items)\n" + strings.Repeat("line\n", 10),
			wantLine:    41,
			wantContain: "calculateTotal",
		},
		{
			name:        "empty oldText returns zero",
			oldText:     "",
			content:     "hello\nworld\n",
			wantLine:    0,
			wantContain: "",
		},
		{
			name:        "whitespace-only oldText returns zero",
			oldText:     "   \n\t  ",
			content:     "hello\nworld\n",
			wantLine:    0,
			wantContain: "",
		},
		{
			name:        "multi-line oldText uses first line",
			oldText:     "func main() {\n\tfmt.Println()\n}",
			content:     "package main\n\nfunc main() {\n\tfmt.Println()\n}\n",
			wantLine:    3,
			wantContain: "func main() {",
		},
		{
			name:        "best match wins over partial",
			oldText:     "handleRequest",
			content:     "line1\nhandleRequestData\nhandleRequest\nhandleSomething\n",
			wantLine:    3,
			wantContain: "handleRequest",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lineNum, lineText := closestLine(tc.oldText, tc.content)
			if lineNum != tc.wantLine {
				t.Errorf("line number = %d, want %d", lineNum, tc.wantLine)
			}
			if tc.wantContain != "" && !strings.Contains(lineText, tc.wantContain) {
				t.Errorf("line text = %q, want to contain %q", lineText, tc.wantContain)
			}
		})
	}
}
