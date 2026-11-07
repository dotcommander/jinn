package jinn

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadCursor_TwoPageConcat(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var body strings.Builder
	for i := 1; i <= 4500; i++ {
		fmt.Fprintf(&body, "row %04d %s\n", i, strings.Repeat("x", i%17))
	}
	content := body.String()
	writeTestFile(t, dir, "pages.txt", content)
	path := filepath.Join(dir, "pages.txt")

	p1, err := e.readFileContent(path, map[string]interface{}{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !p1.Truncated || p1.NextLine == 0 || p1.NextOffset <= 0 {
		t.Fatalf("page 1 lacks a cursor: %+v", p1)
	}

	// next_offset must land exactly on the byte start of NextLine: the
	// concatenation of pages must equal the file bytes.
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	var prefix int
	for i := 0; i < p1.NextLine-1; i++ {
		prefix += len(lines[i]) + 1
	}
	if p1.NextOffset != int64(prefix) {
		t.Fatalf("next_offset=%d, want %d (byte start of line %d)", p1.NextOffset, prefix, p1.NextLine)
	}

	p2, err := e.readFileContent(path, map[string]interface{}{"start_line": float64(p1.NextLine)}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !p2.Truncated || p2.NextOffset <= 0 && p2.NextLine > 0 {
		t.Fatalf("page 2 lacks its cursor: %+v", p2)
	}

	// Page boundaries: page1 ends at nextLine-1, page2 starts at nextLine.
	if !strings.HasSuffix(p1.Content, lines[p1.NextLine-2]) {
		t.Fatalf("page 1 does not end with line %d", p1.NextLine-1)
	}
	wantLine := lines[p1.NextLine-1]
	if !strings.Contains(p2.Content, wantLine) {
		t.Fatalf("page 2 does not start with line %d", p1.NextLine)
	}

	if p1.TempFile != "" || p2.TempFile != "" {
		t.Fatal("cursor reads must not spill")
	}
	// The digest is computed by the streaming pass but only surfaced when
	// the caller requested it.
	if p1.Checksum != "" {
		t.Error("digest must not surface when not requested")
	}
}
