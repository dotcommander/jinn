package jinn

import (
	"fmt"
	"strings"
	"testing"
)

// makeGoSource generates valid-ish Go source with n functions, each having
// linesPerFunc lines including the func signature, braces, and body lines.
func makeGoSource(n, linesPerFunc int) string {
	var b strings.Builder
	b.WriteString("package main\n\n")
	for i := range n {
		fmt.Fprintf(&b, "func func%d() {\n", i)
		// Body lines: linesPerFunc - 2 (opening + closing brace)
		bodyLines := linesPerFunc - 2
		if bodyLines < 0 {
			bodyLines = 0
		}
		for j := range bodyLines {
			fmt.Fprintf(&b, "\tstmt%d()\n", j)
		}
		b.WriteString("}\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// braceDepth computes the brace nesting depth at the end of the given content,
// ignoring braces inside string literals (double-quoted) and line comments.
func braceDepth(content string) int {
	depth := 0
	inString := false
	inComment := false
	inRawString := false
	for i := 0; i < len(content); i++ {
		ch := content[i]
		if inRawString {
			if ch == '`' {
				inRawString = false
			}
			continue
		}
		if inString {
			if ch == '\\' {
				i++ // skip escaped char
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if inComment {
			if ch == '\n' {
				inComment = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '`':
			inRawString = true
		case '/':
			if i+1 < len(content) && content[i+1] == '/' {
				inComment = true
				i++ // skip second slash
			}
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	return depth
}

func TestTruncateOutputSmart_FitsWithinLimit(t *testing.T) {
	t.Parallel()
	src := makeGoSource(10, 5) // 10 functions, 5 lines each
	result := truncateOutputSmart(src, 100, ".go")
	if result.Truncated {
		t.Error("should not be truncated when content fits within limit")
	}
	if result.TotalLines != 50 {
		// 10 funcs * (package+blank=2 header lines + 5 lines each) ≈ depends on generation
		// Let's count actual lines from the generated source
		actual := len(strings.Split(src, "\n"))
		if len(strings.Split(src, "\n")) > 0 && strings.Split(src, "\n")[len(strings.Split(src, "\n"))-1] == "" {
			actual--
		}
		if result.TotalLines != actual {
			t.Errorf("TotalLines = %d, want %d", result.TotalLines, actual)
		}
	}
	if result.Content != src {
		t.Error("Content should equal input when not truncated")
	}
}

func TestTruncateOutputSmart_TruncatesAtBoundary(t *testing.T) {
	t.Parallel()
	// 10 functions × 50 lines each = 500 lines
	src := makeGoSource(10, 50)
	result := truncateOutputSmart(src, 200, ".go")

	if !result.Truncated {
		t.Fatal("expected Truncated=true")
	}
	if result.ShownLines >= 500 {
		t.Errorf("ShownLines = %d, should be < 500", result.ShownLines)
	}
	if result.ShownLines > 200 {
		t.Errorf("ShownLines = %d, should be <= 200 (limit)", result.ShownLines)
	}
	if !strings.Contains(result.Content, "[... truncated:") {
		t.Errorf("Content should contain truncation marker, got tail: %s",
			result.Content[max(0, len(result.Content)-200):])
	}
	// Verify complete functions: brace depth at end should be 0
	depth := braceDepth(result.Content)
	if depth != 0 {
		t.Errorf("brace depth at end = %d, want 0 (functions should not be cut in half)", depth)
	}
}

func TestTruncateOutputSmart_NestedFunctions(t *testing.T) {
	t.Parallel()
	// Build Go source with deeply nested structures
	var b strings.Builder
	b.WriteString("package main\n\n")
	b.WriteString("func outer() {\n")
	b.WriteString("\ttype inner struct {\n")
	b.WriteString("\t\tfield int\n")
	b.WriteString("\t}\n")
	for i := range 50 {
		fmt.Fprintf(&b, "\tinner := func() {\n")
		fmt.Fprintf(&b, "\t\t_ = %d\n", i)
		// nested struct
		fmt.Fprintf(&b, "\t\ttype deep struct {\n")
		fmt.Fprintf(&b, "\t\t\tval int\n")
		fmt.Fprintf(&b, "\t\t}\n")
		fmt.Fprintf(&b, "\t\t_ = deep{}\n")
		b.WriteString("\t}\n")
		fmt.Fprintf(&b, "\t_ = inner\n")
	}
	b.WriteString("}\n")

	src := b.String()
	// Limit that would cut inside a nested block
	result := truncateOutputSmart(src, 30, ".go")

	if !result.Truncated {
		t.Fatal("expected Truncated=true")
	}
	depth := braceDepth(result.Content)
	if depth != 0 {
		t.Errorf("brace depth at end = %d, want 0 — cut should be at enclosing top-level boundary", depth)
	}
}

func TestTruncateOutputSmart_UnclosedBrace(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("package main\n\n")
	b.WriteString("func broken() {\n")
	for i := range 100 {
		fmt.Fprintf(&b, "\tstmt%d()\n", i)
	}
	// No closing brace — malformed Go
	src := b.String()

	result := truncateOutputSmart(src, 30, ".go")
	// Should fall back gracefully — must not panic
	if !result.Truncated {
		t.Error("expected Truncated=true for malformed input that exceeds limit")
	}
	if len(result.Content) == 0 {
		t.Error("Content should not be empty on graceful fallback")
	}
}

func TestTruncateOutputSmart_NonCSyntaxFallback(t *testing.T) {
	t.Parallel()
	var lines []string
	for i := range 500 {
		lines = append(lines, fmt.Sprintf("key%d: value%d", i, i))
	}
	yaml := strings.Join(lines, "\n")

	result := truncateOutputSmart(yaml, 200, ".yaml")
	if !result.Truncated {
		t.Fatal("expected Truncated=true for YAML over limit")
	}
	if result.ShownLines > 200 {
		t.Errorf("ShownLines = %d, should be <= 200 for head-style fallback", result.ShownLines)
	}
	// Head strategy keeps the first lines
	if !strings.Contains(result.Content, "key0: value0") {
		t.Error("head fallback should contain first line")
	}
}

func TestTruncateOutputSmart_EmptyFile(t *testing.T) {
	t.Parallel()
	result := truncateOutputSmart("", 100, ".go")
	if result.Truncated {
		t.Error("empty input should not be truncated")
	}
	if result.TotalLines != 0 {
		t.Errorf("TotalLines = %d, want 0", result.TotalLines)
	}
	if result.Content != "" {
		t.Errorf("Content should be empty, got %q", result.Content)
	}
}

func TestTruncateOutputSmart_SingleFunction(t *testing.T) {
	t.Parallel()
	// Single 10-line function, limit=5 — can't cut within the function
	src := "package main\n\nfunc only() {\n\tstmt0()\n\tstmt1()\n\tstmt2()\n\tstmt3()\n\tstmt4()\n\tstmt5()\n}"
	result := truncateOutputSmart(src, 5, ".go")

	if !result.Truncated {
		// If not truncated, the whole function was kept despite exceeding limit
		// which is acceptable behavior for smart truncation
		return
	}
	// If truncated, it must still be valid (depth 0) or use head fallback
	depth := braceDepth(result.Content)
	if depth != 0 {
		t.Errorf("brace depth at end = %d, want 0 — should not cut mid-function", depth)
	}
}

func TestTruncateOutputSmart_CommentsAndStrings(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("package main\n\n")
	for i := range 20 {
		fmt.Fprintf(&b, "func func%d() {\n", i)
		// Braces inside string literals — should NOT count for depth
		b.WriteString("\ts := \"{ {{{}} }\"\n")
		// Braces inside comments — should NOT count for depth
		b.WriteString("\t// { { { nested braces in comment\n")
		b.WriteString("\t_ = s\n")
		b.WriteString("}\n\n")
	}
	src := strings.TrimRight(b.String(), "\n")

	result := truncateOutputSmart(src, 30, ".go")
	if !result.Truncated {
		t.Fatal("expected Truncated=true")
	}
	depth := braceDepth(result.Content)
	if depth != 0 {
		t.Errorf("brace depth at end = %d, want 0 — braces in strings/comments should not affect truncation boundary", depth)
	}
}

func TestIsCSyntaxExt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ext  string
		want bool
	}{
		{".go", true},
		{".java", true},
		{".c", true},
		{".cpp", true},
		{".h", true},
		{".hpp", true},
		{".rs", true},
		{".ts", true},
		{".tsx", true},
		{".js", true},
		{".jsx", true},
		{".yaml", false},
		{".py", false},
		{".md", false},
		{".txt", false},
		{"", false},
		{"go", false},
	}
	for _, tc := range cases {
		if got := isCSyntaxExt(tc.ext); got != tc.want {
			t.Errorf("isCSyntaxExt(%q) = %v, want %v", tc.ext, got, tc.want)
		}
	}
}
