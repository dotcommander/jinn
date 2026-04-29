package jinn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMultiEdit_Basic(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "m1.txt", "aaa\n")
	writeTestFile(t, dir, "m2.txt", "bbb\n")
	edits := []interface{}{
		map[string]interface{}{"path": "m1.txt", "old_text": "aaa", "new_text": "AAA"},
		map[string]interface{}{"path": "m2.txt", "old_text": "bbb", "new_text": "BBB"},
	}
	result, err := e.multiEdit(args("edits", edits))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "applied 2 edits") {
		t.Errorf("expected success, got: %s", result.Text)
	}
	d1, _ := os.ReadFile(filepath.Join(dir, "m1.txt"))
	d2, _ := os.ReadFile(filepath.Join(dir, "m2.txt"))
	if string(d1) != "AAA\n" || string(d2) != "BBB\n" {
		t.Errorf("files not updated: m1=%q m2=%q", d1, d2)
	}
}

func TestMultiEdit_ValidationFailureAbortsAll(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "ok.txt", "good\n")
	writeTestFile(t, dir, "bad.txt", "original\n")
	edits := []interface{}{
		map[string]interface{}{"path": "ok.txt", "old_text": "good", "new_text": "GOOD"},
		map[string]interface{}{"path": "bad.txt", "old_text": "MISSING", "new_text": "x"},
	}
	_, err := e.multiEdit(args("edits", edits))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected validation error, got: %v", err)
	}
	d, _ := os.ReadFile(filepath.Join(dir, "ok.txt"))
	if string(d) != "good\n" {
		t.Errorf("ok.txt should be unchanged after failed multi_edit, got: %q", d)
	}
}

func TestMultiEdit_EmptyEdits(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.multiEdit(args("edits", []interface{}{}))
	if err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Errorf("expected non-empty error, got: %v", err)
	}
}

func TestMultiEdit_FuzzyAndCRLF(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "multi.txt", "aaa\r\nbbb\r\nccc\r\n")
	result, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{
			"path":     "multi.txt",
			"old_text": "bbb",
			"new_text": "BBB",
		},
	}))
	if err != nil {
		t.Fatalf("multi_edit failed: %v", err)
	}
	if strings.Contains(result.Text, "error") {
		t.Fatalf("multi_edit returned error in result: %s", result.Text)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "multi.txt"))
	content := string(data)
	if !strings.Contains(content, "\r\n") {
		t.Error("CRLF should be preserved in multi_edit")
	}
	if !strings.Contains(content, "BBB") {
		t.Error("edit should have been applied")
	}
}

func TestMultiEdit_FuzzyIndent(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	content := "func main() {\n\tx := 1\n\ty := 2\n}\n"
	writeTestFile(t, dir, "mi_indent.go", content)

	result, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{
			"path":         "mi_indent.go",
			"old_text":     "x := 1",
			"new_text":     "x := 42\n\tz := x + 1",
			"fuzzy_indent": true,
		},
	}))
	if err != nil {
		t.Fatalf("multi_edit with fuzzy_indent failed: %v", err)
	}
	if strings.Contains(result.Text, "error") {
		t.Fatalf("multi_edit returned error: %s", result.Text)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "mi_indent.go"))
	s := string(data)
	if !strings.Contains(s, "\tx := 42") {
		t.Errorf("replacement should have tab indent from fuzzy_indent, got:\n%s", s)
	}
	if !strings.Contains(s, "\tz := x + 1") {
		t.Errorf("second line should have tab indent, got:\n%s", s)
	}
	if !strings.Contains(s, "\ty := 2") {
		t.Error("unchanged line should still have tab indent")
	}
}

func TestMultiEdit_SameFileMultipleEdits(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "same.txt", "alpha\nbravo\ncharlie\ndelta\necho\n")

	edits := []interface{}{
		map[string]interface{}{"path": "same.txt", "old_text": "alpha", "new_text": "ALPHA"},
		map[string]interface{}{"path": "same.txt", "old_text": "charlie", "new_text": "CHARLIE"},
		map[string]interface{}{"path": "same.txt", "old_text": "echo", "new_text": "ECHO"},
	}
	result, err := e.multiEdit(args("edits", edits))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "applied 3 edits") {
		t.Errorf("expected 3 edits applied, got: %s", result.Text)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "same.txt"))
	s := string(data)
	// All three edits must be present — previously only the last survived.
	if !strings.Contains(s, "ALPHA") {
		t.Errorf("first edit missing, got:\n%s", s)
	}
	if !strings.Contains(s, "CHARLIE") {
		t.Errorf("second edit missing, got:\n%s", s)
	}
	if !strings.Contains(s, "ECHO") {
		t.Errorf("third edit missing, got:\n%s", s)
	}
	// Unchanged lines must survive.
	if !strings.Contains(s, "bravo") || !strings.Contains(s, "delta") {
		t.Errorf("unchanged lines lost, got:\n%s", s)
	}
}

func TestMultiEdit_SameFileSequentialDependent(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "chain.txt", "one\n")

	// Second edit depends on first edit's output being in the file.
	edits := []interface{}{
		map[string]interface{}{"path": "chain.txt", "old_text": "one", "new_text": "one\ntwo"},
		map[string]interface{}{"path": "chain.txt", "old_text": "two", "new_text": "TWO"},
	}
	_, err := e.multiEdit(args("edits", edits))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "chain.txt"))
	s := string(data)
	if !strings.Contains(s, "one") {
		t.Errorf("original line missing, got:\n%s", s)
	}
	if !strings.Contains(s, "TWO") {
		t.Errorf("dependent edit not applied, got:\n%s", s)
	}
}

func TestMultiEdit_SensitivePath(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, ".env", "SECRET=abc\n")
	_, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{"path": ".env", "old_text": "SECRET", "new_text": "SAFE"},
	}))
	if err == nil || !strings.Contains(err.Error(), "sensitive path") {
		t.Errorf("expected sensitive path error, got: %v", err)
	}
}

func TestMultiEdit_OutsideWorkdir(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{"path": "/etc/passwd", "old_text": "root", "new_text": "x"},
	}))
	if err == nil || !strings.Contains(err.Error(), "outside working directory") {
		t.Errorf("expected outside workdir error, got: %v", err)
	}
}

func TestMultiEdit_FileNotFound(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{"path": "nonexistent.txt", "old_text": "x", "new_text": "y"},
	}))
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	if !strings.Contains(err.Error(), "edit[0]") {
		t.Errorf("error should reference edit index, got: %v", err)
	}
	if !strings.Contains(err.Error(), "nonexistent.txt") {
		t.Errorf("error should reference filename, got: %v", err)
	}
}

func TestMultiEdit_ErrorIncludesIndex(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{"path": "a.txt", "old_text": "x", "new_text": "y"},
		map[string]interface{}{"path": "b.txt", "old_text": "x", "new_text": "y"},
	}))
	// First edit's file doesn't exist either, but edit[0] is checked first.
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "edit[0]") {
		t.Errorf("error should reference edit index, got: %v", err)
	}
}

func TestMultiEdit_NilEdits(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.multiEdit(args("edits", nil))
	if err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Errorf("expected non-empty array error, got: %v", err)
	}
}

func TestMultiEdit_EditsNotArray(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.multiEdit(args("edits", "not an array"))
	if err == nil || !strings.Contains(err.Error(), "non-empty array") {
		t.Errorf("expected non-empty array error, got: %v", err)
	}
}

func TestMultiEdit_InvalidEntryFormat(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.multiEdit(args("edits", []interface{}{
		"not a map",
	}))
	if err == nil || !strings.Contains(err.Error(), "edit[0]: invalid format") {
		t.Errorf("expected format error, got: %v", err)
	}
}

func TestMultiEdit_StaleFile(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	fp := filepath.Join(dir, "stale.txt")
	writeTestFile(t, dir, "stale.txt", "original\n")

	// Read the file so the tracker records its mtime.
	e.readFile(args("path", "stale.txt"))

	// External modification — advance mtime beyond the tracked value.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(fp, []byte("changed!\n"), 0o644)

	_, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{"path": "stale.txt", "old_text": "original", "new_text": "NEW"},
	}))
	if err == nil || !strings.Contains(err.Error(), "modified since last read") {
		t.Errorf("expected stale error, got: %v", err)
	}
}

func TestMultiEdit_SingleEdit(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "solo.txt", "hello world\n")
	result, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{"path": "solo.txt", "old_text": "hello", "new_text": "goodbye"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "applied 1 edits") {
		t.Errorf("expected 'applied 1 edits', got: %s", result.Text)
	}
	d, _ := os.ReadFile(filepath.Join(dir, "solo.txt"))
	if string(d) != "goodbye world\n" {
		t.Errorf("content = %q, want %q", d, "goodbye world\n")
	}
}

func TestMultiEdit_BOMPreserved(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "bom.txt", "\xEF\xBB\xBFhello\n")
	result, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{"path": "bom.txt", "old_text": "hello", "new_text": "world"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Text, "error") {
		t.Fatalf("result contains error: %s", result.Text)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "bom.txt"))
	if !strings.HasPrefix(string(data), "\xEF\xBB\xBF") {
		t.Error("BOM should be preserved")
	}
	if !strings.Contains(string(data), "world") {
		t.Error("edit should have been applied")
	}
}

func TestMultiEdit_ShowContext(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "ctx.txt", "line1\nline2\nline3\nline4\nline5\n")
	result, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{
			"path": "ctx.txt", "old_text": "line3", "new_text": "CHANGED",
			"show_context": float64(1),
		},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "context ---") {
		t.Errorf("expected context section, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "line2") {
		t.Errorf("context should show line before edit, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "CHANGED") {
		t.Errorf("context should show changed line, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "line4") {
		t.Errorf("context should show line after edit, got: %s", result.Text)
	}
}

func TestMultiEdit_ManyEdits(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	var edits []interface{}
	for i := range 5 {
		name := fmt.Sprintf("f%d.txt", i)
		writeTestFile(t, dir, name, fmt.Sprintf("val%d\n", i))
		edits = append(edits, map[string]interface{}{
			"path": name, "old_text": fmt.Sprintf("val%d", i), "new_text": fmt.Sprintf("VAL%d", i),
		})
	}
	result, err := e.multiEdit(args("edits", edits))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "applied 5 edits") {
		t.Errorf("expected 'applied 5 edits', got: %s", result.Text)
	}
	for i := range 5 {
		d, _ := os.ReadFile(filepath.Join(dir, fmt.Sprintf("f%d.txt", i)))
		want := fmt.Sprintf("VAL%d\n", i)
		if string(d) != want {
			t.Errorf("f%d.txt = %q, want %q", i, d, want)
		}
	}
}

func TestMultiEdit_MixedFuzzyAndExact(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	// Smart quotes in file, straight quotes in old_text.
	writeTestFile(t, dir, "smart.txt", "\u201Chello\u201D\n")
	writeTestFile(t, dir, "plain.txt", "world\n")
	result, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{"path": "smart.txt", "old_text": `"hello"`, "new_text": `"hi"`},
		map[string]interface{}{"path": "plain.txt", "old_text": "world", "new_text": "WORLD"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Text, "fuzzy match") {
		t.Error("expected fuzzy match indicator for smart quotes edit")
	}
	// Verify both files updated.
	// After fuzzy match, content is normalized — straight quotes replace smart quotes.
	smart, _ := os.ReadFile(filepath.Join(dir, "smart.txt"))
	if !strings.Contains(string(smart), `"hi"`) {
		t.Errorf("smart quotes file not updated, got: %q", smart)
	}
	plain, _ := os.ReadFile(filepath.Join(dir, "plain.txt"))
	if string(plain) != "WORLD\n" {
		t.Errorf("plain file = %q, want %q", plain, "WORLD\n")
	}
}

func TestMultiEdit_DiffMetaMultiFile(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "d1.txt", "alpha\n")
	writeTestFile(t, dir, "d2.txt", "beta\n")
	result, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{"path": "d1.txt", "old_text": "alpha", "new_text": "ALPHA"},
		map[string]interface{}{"path": "d2.txt", "old_text": "beta", "new_text": "BETA"},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	edit, ok := result.Meta["edit"].(editDetails)
	if !ok {
		t.Fatalf("expected editDetails in Meta, got: %T", result.Meta["edit"])
	}
	if edit.Diff == "" {
		t.Error("expected non-empty diff")
	}
	if !strings.Contains(edit.Diff, "- alpha") {
		t.Errorf("diff should show removed alpha, got: %s", edit.Diff)
	}
	if !strings.Contains(edit.Diff, "- beta") {
		t.Errorf("diff should show removed beta, got: %s", edit.Diff)
	}
	if !strings.Contains(edit.Diff, "+ ALPHA") {
		t.Errorf("diff should show added ALPHA, got: %s", edit.Diff)
	}
}

func TestMultiEdit_MissingEditsKey(t *testing.T) {
	t.Parallel()
	e, _ := testEngine(t)
	_, err := e.multiEdit(map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "non-empty array") {
		t.Errorf("expected non-empty array error, got: %v", err)
	}
}

func TestMultiEdit_FuzzyIndentMultiFile(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "tabs.go", "func main() {\n\tx := 1\n}\n")
	writeTestFile(t, dir, "spaces.go", "func main() {\n    y := 2\n}\n")

	_, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{
			"path": "tabs.go", "old_text": "x := 1", "new_text": "x := 42\nz := x",
			"fuzzy_indent": true,
		},
		map[string]interface{}{
			"path": "spaces.go", "old_text": "y := 2", "new_text": "y := 99\nw := y",
			"fuzzy_indent": true,
		},
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tabs, _ := os.ReadFile(filepath.Join(dir, "tabs.go"))
	if !strings.Contains(string(tabs), "\tx := 42") {
		t.Errorf("tabs.go should have tab-indented replacement, got:\n%s", tabs)
	}
	spaces, _ := os.ReadFile(filepath.Join(dir, "spaces.go"))
	if !strings.Contains(string(spaces), "    y := 99") {
		t.Errorf("spaces.go should have 4-space-indented replacement, got:\n%s", spaces)
	}
}

func TestMultiEdit_ValidationFailsOnSecondIndex(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "v1.txt", "exists\n")
	edits := []interface{}{
		map[string]interface{}{"path": "v1.txt", "old_text": "exists", "new_text": "EXISTS"},
		map[string]interface{}{"path": "v2.txt", "old_text": "ghost", "new_text": "GHOST"},
	}
	_, err := e.multiEdit(args("edits", edits))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "edit[1]") {
		t.Errorf("error should reference edit[1], got: %v", err)
	}
	// Phase 1 validates all before applying — v1.txt must be untouched.
	d, _ := os.ReadFile(filepath.Join(dir, "v1.txt"))
	if string(d) != "exists\n" {
		t.Errorf("v1.txt should be unchanged after validation failure, got: %q", d)
	}
}

func TestMultiEdit_AmbiguousMatchInOneEdit(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "ambig.txt", "dup\nother\ndup\n")
	writeTestFile(t, dir, "clean.txt", "unique\n")
	edits := []interface{}{
		map[string]interface{}{"path": "clean.txt", "old_text": "unique", "new_text": "UNIQUE"},
		map[string]interface{}{"path": "ambig.txt", "old_text": "dup", "new_text": "replaced"},
	}
	_, err := e.multiEdit(args("edits", edits))
	if err == nil {
		t.Fatal("expected error for ambiguous match")
	}
	if !strings.Contains(err.Error(), "matches 2 locations") {
		t.Errorf("expected ambiguity error, got: %v", err)
	}
	// clean.txt must be untouched — Phase 1 validates all before Phase 2.
	d, _ := os.ReadFile(filepath.Join(dir, "clean.txt"))
	if string(d) != "unique\n" {
		t.Errorf("clean.txt should be unchanged, got: %q", d)
	}
}

func TestMultiEdit_FuzzyIndentDefaultFalse(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	content := "func main() {\n\tx := 1\n}\n"
	writeTestFile(t, dir, "mi_noindent.go", content)

	_, err := e.multiEdit(args("edits", []interface{}{
		map[string]interface{}{
			"path":     "mi_noindent.go",
			"old_text": "x := 1",
			"new_text": "a := 1\nb := 2",
		},
	}))
	if err != nil {
		t.Fatalf("multi_edit failed: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "mi_noindent.go"))
	s := string(data)
	if !strings.Contains(s, "a := 1\nb := 2") {
		t.Errorf("replacement should not be re-indented without fuzzy_indent, got:\n%s", s)
	}
}
