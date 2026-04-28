package jinn

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePatch_Empty(t *testing.T) {
	t.Parallel()
	_, err := parsePatch("")
	if err == nil {
		t.Fatal("expected error for empty patch")
	}
}

func TestParsePatch_MissingBegin(t *testing.T) {
	t.Parallel()
	_, err := parsePatch("*** End Patch")
	if err == nil {
		t.Fatal("expected error for missing begin marker")
	}
}

func TestParsePatch_MissingEnd(t *testing.T) {
	t.Parallel()
	_, err := parsePatch("*** Begin Patch\n*** Add File: test.txt\n+hello\n")
	if err == nil {
		t.Fatal("expected error for missing end marker")
	}
}

func TestParsePatch_AddFile(t *testing.T) {
	t.Parallel()
	ops, err := parsePatch("*** Begin Patch\n*** Add File: hello.txt\n+line1\n+line2\n*** End Patch")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if ops[0].kind != "add" {
		t.Fatalf("expected add, got %s", ops[0].kind)
	}
	if ops[0].path != "hello.txt" {
		t.Fatalf("expected hello.txt, got %s", ops[0].path)
	}
	want := "line1\nline2\n"
	if ops[0].contents != want {
		t.Fatalf("expected %q, got %q", want, ops[0].contents)
	}
}

func TestParsePatch_DeleteFile(t *testing.T) {
	t.Parallel()
	ops, err := parsePatch("*** Begin Patch\n*** Delete File: old.txt\n*** End Patch")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if ops[0].kind != "delete" {
		t.Fatalf("expected delete, got %s", ops[0].kind)
	}
}

func TestParsePatch_UpdateFile(t *testing.T) {
	t.Parallel()
	patch := `*** Begin Patch
*** Update File: main.go
@@ func main() {
  func main() {
-   fmt.Println("old")
+   fmt.Println("new")
  }
*** End Patch`
	ops, err := parsePatch(patch)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	if ops[0].kind != "update" {
		t.Fatalf("expected update, got %s", ops[0].kind)
	}
	if len(ops[0].chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(ops[0].chunks))
	}
	chunk := ops[0].chunks[0]
	if chunk.context != "func main() {" {
		t.Fatalf("expected context 'func main() {', got %q", chunk.context)
	}
	// oldLines: "  func main() {", "   fmt.Println(\"old\")", "  }"
	// newLines: "  func main() {", "   fmt.Println(\"new\")", "  }"
	if len(chunk.oldLines) != 3 {
		t.Fatalf("expected 3 oldLines, got %d: %v", len(chunk.oldLines), chunk.oldLines)
	}
	if len(chunk.newLines) != 3 {
		t.Fatalf("expected 3 newLines, got %d: %v", len(chunk.newLines), chunk.newLines)
	}
}

func TestParsePatch_MultipleOps(t *testing.T) {
	t.Parallel()
	patch := `*** Begin Patch
*** Add File: new.txt
+hello world
*** Delete File: old.txt
*** Update File: main.go
@@ line1
 line1
-line2
+LINE2
*** End Patch`
	ops, err := parsePatch(patch)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(ops) != 3 {
		t.Fatalf("expected 3 ops, got %d", len(ops))
	}
	if ops[0].kind != "add" || ops[1].kind != "delete" || ops[2].kind != "update" {
		t.Fatalf("expected add/delete/update, got %s/%s/%s", ops[0].kind, ops[1].kind, ops[2].kind)
	}
}

func TestParsePatch_InvalidHeader(t *testing.T) {
	t.Parallel()
	patch := `*** Begin Patch
*** Unknown: foo
*** End Patch`
	_, err := parsePatch(patch)
	if err == nil {
		t.Fatal("expected error for invalid header")
	}
}

func TestParsePatch_MoveToRejected(t *testing.T) {
	t.Parallel()
	patch := `*** Begin Patch
*** Update File: main.go
*** Move to: renamed.go
*** End Patch`
	_, err := parsePatch(patch)
	if err == nil {
		t.Fatal("expected error for move-to operation")
	}
}

func TestSeekSequence_Exact(t *testing.T) {
	t.Parallel()
	lines := []string{"a", "b", "c", "d", "e"}
	idx := seekSequence(lines, []string{"c", "d"}, 0, false)
	if idx != 2 {
		t.Fatalf("expected 2, got %d", idx)
	}
}

func TestSeekSequence_EOF(t *testing.T) {
	t.Parallel()
	lines := []string{"a", "b", "c", "d", "e"}
	idx := seekSequence(lines, []string{"d", "e"}, 0, true)
	if idx != 3 {
		t.Fatalf("expected 3, got %d", idx)
	}
}

func TestSeekSequence_NotFound(t *testing.T) {
	t.Parallel()
	lines := []string{"a", "b", "c"}
	idx := seekSequence(lines, []string{"z"}, 0, false)
	if idx != -1 {
		t.Fatalf("expected -1, got %d", idx)
	}
}

func TestSeekSequence_TrimMatch(t *testing.T) {
	t.Parallel()
	lines := []string{"  hello  "}
	idx := seekSequence(lines, []string{"hello"}, 0, false)
	if idx != 0 {
		t.Fatalf("expected 0 (trim match), got %d", idx)
	}
}

func TestDeriveUpdatedContent_SimpleReplace(t *testing.T) {
	t.Parallel()
	content := "line1\nline2\nline3\n"
	chunks := []updateChunk{
		{
			oldLines: []string{"line2"},
			newLines: []string{"LINE2"},
		},
	}
	result, err := deriveUpdatedContent("test.txt", content, chunks)
	if err != nil {
		t.Fatalf("derive failed: %v", err)
	}
	want := "line1\nLINE2\nline3\n"
	if result != want {
		t.Fatalf("expected %q, got %q", want, result)
	}
}

func TestDeriveUpdatedContent_WithContext(t *testing.T) {
	t.Parallel()
	content := "a\nb\nc\nd\ne\n"
	chunks := []updateChunk{
		{
			context:  "c",
			oldLines: []string{"c", "d"},
			newLines: []string{"C", "D"},
		},
	}
	result, err := deriveUpdatedContent("test.txt", content, chunks)
	if err != nil {
		t.Fatalf("derive failed: %v", err)
	}
	want := "a\nb\nC\nD\ne\n"
	if result != want {
		t.Fatalf("expected %q, got %q", want, result)
	}
}

func TestDeriveUpdatedContent_MultipleChunks(t *testing.T) {
	t.Parallel()
	content := "a\nb\nc\nd\ne\nf\n"
	chunks := []updateChunk{
		{
			oldLines: []string{"b"},
			newLines: []string{"B"},
		},
		{
			oldLines: []string{"e"},
			newLines: []string{"E"},
		},
	}
	result, err := deriveUpdatedContent("test.txt", content, chunks)
	if err != nil {
		t.Fatalf("derive failed: %v", err)
	}
	want := "a\nB\nc\nd\nE\nf\n"
	if result != want {
		t.Fatalf("expected %q, got %q", want, result)
	}
}

func TestDeriveUpdatedContent_EOF(t *testing.T) {
	t.Parallel()
	content := "a\nb\nc\n"
	chunks := []updateChunk{
		{
			oldLines: []string{"c"},
			newLines: []string{"C"},
			isEOF:    true,
		},
	}
	result, err := deriveUpdatedContent("test.txt", content, chunks)
	if err != nil {
		t.Fatalf("derive failed: %v", err)
	}
	want := "a\nb\nC\n"
	if result != want {
		t.Fatalf("expected %q, got %q", want, result)
	}
}

func TestDeriveUpdatedContent_InvalidContext(t *testing.T) {
	t.Parallel()
	content := "a\nb\nc\n"
	chunks := []updateChunk{
		{
			context:  "not_found",
			oldLines: []string{"a"},
			newLines: []string{"A"},
		},
	}
	_, err := deriveUpdatedContent("test.txt", content, chunks)
	if err == nil {
		t.Fatal("expected error for missing context")
	}
}

func TestApplyPatch_AddFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	e := New(dir)

	result, err := e.applyPatch(map[string]interface{}{
		"patch": "*** Begin Patch\n*** Add File: new.txt\n+hello world\n*** End Patch",
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	data, err := os.ReadFile(filepath.Join(dir, "new.txt"))
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(data) != "hello world\n" {
		t.Fatalf("expected 'hello world\\n', got %q", string(data))
	}
}

func TestApplyPatch_DeleteFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "doomed.txt")
	if err := os.WriteFile(target, []byte("bye\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := New(dir)
	result, err := e.applyPatch(map[string]interface{}{
		"patch": "*** Begin Patch\n*** Delete File: doomed.txt\n*** End Patch",
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("expected file to be deleted")
	}
}

func TestApplyPatch_UpdateFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	content := "package main\n\nfunc main() {\n\tfmt.Println(\"old\")\n}\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	e := New(dir)
	patch := `*** Begin Patch
*** Update File: main.go
@@ func main() {
 func main() {
-	fmt.Println("old")
+	fmt.Println("new")
 }
*** End Patch`
	result, err := e.applyPatch(map[string]interface{}{
		"patch": patch,
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	want := "package main\n\nfunc main() {\n\tfmt.Println(\"new\")\n}\n"
	if string(data) != want {
		t.Fatalf("expected %q, got %q", want, string(data))
	}
}

func TestApplyPatch_DryRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "main.go")
	content := "package main\n\nfunc main() {\n\tfmt.Println(\"old\")\n}\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	e := New(dir)
	patch := `*** Begin Patch
*** Update File: main.go
@@ func main() {
 func main() {
-	fmt.Println("old")
+	fmt.Println("new")
 }
*** End Patch`
	result, err := e.applyPatch(map[string]interface{}{
		"patch":   patch,
		"dry_run": true,
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if !contains(result.Text, "[dry-run]") {
		t.Fatalf("expected [dry-run] prefix, got %q", result.Text)
	}

	// File should be unchanged.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(data) != content {
		t.Fatalf("file should be unchanged after dry run")
	}
}

func TestApplyPatch_DeleteNonExistent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	e := New(dir)

	_, err := e.applyPatch(map[string]interface{}{
		"patch": "*** Begin Patch\n*** Delete File: ghost.txt\n*** End Patch",
	})
	if err == nil {
		t.Fatal("expected error for deleting non-existent file")
	}
}

func TestApplyPatch_UpdateNonExistent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	e := New(dir)

	_, err := e.applyPatch(map[string]interface{}{
		"patch": "*** Begin Patch\n*** Update File: ghost.txt\n@@ line1\n-old\n+new\n*** End Patch",
	})
	if err == nil {
		t.Fatal("expected error for updating non-existent file")
	}
}

func TestApplyPatch_MixedOperations(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create an existing file to update.
	updateTarget := filepath.Join(dir, "update.txt")
	if err := os.WriteFile(updateTarget, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create an existing file to delete.
	deleteTarget := filepath.Join(dir, "delete.txt")
	if err := os.WriteFile(deleteTarget, []byte("bye\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := New(dir)
	patch := `*** Begin Patch
*** Add File: created.txt
+new file
*** Delete File: delete.txt
*** Update File: update.txt
@@ line2
 line2
-line3
+LINE3
*** End Patch`
	result, err := e.applyPatch(map[string]interface{}{
		"patch": patch,
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if !contains(result.Text, "3 operation") {
		t.Fatalf("expected 3 operations, got %q", result.Text)
	}

	// Verify add.
	data, err := os.ReadFile(filepath.Join(dir, "created.txt"))
	if err != nil || string(data) != "new file\n" {
		t.Fatalf("add failed: %q, %v", string(data), err)
	}
	// Verify delete.
	if _, err := os.Stat(deleteTarget); !os.IsNotExist(err) {
		t.Fatal("delete failed")
	}
	// Verify update.
	data, err = os.ReadFile(updateTarget)
	if err != nil {
		t.Fatalf("read update target: %v", err)
	}
	if string(data) != "line1\nline2\nLINE3\n" {
		t.Fatalf("update failed, got %q", string(data))
	}
}

func TestApplyPatch_EOFMarker(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(target, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e := New(dir)
	patch := `*** Begin Patch
*** Update File: test.txt
@@
 a
 b
-c
+C
*** End of File
*** End Patch`
	_, err := e.applyPatch(map[string]interface{}{
		"patch": patch,
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(data) != "a\nb\nC\n" {
		t.Fatalf("expected 'a\\nb\\nC\\n', got %q", string(data))
	}
}

func TestApplyPatch_BOMPreserved(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "bom.txt")
	content := "\xEF\xBB\xBFline1\nline2\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	e := New(dir)
	patch := `*** Begin Patch
*** Update File: bom.txt
@@ line2
 line2
+line3
*** End Patch`
	_, err := e.applyPatch(map[string]interface{}{
		"patch": patch,
	})
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	// BOM should be preserved.
	if !startsWith(string(data), "\xEF\xBB\xBF") {
		t.Fatalf("BOM not preserved, got %q", string(data)[:20])
	}
	if string(data) != "\xEF\xBB\xBFline1\nline2\nline3\n" {
		t.Fatalf("expected BOM+content, got %q", string(data))
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// startsWith checks if s starts with prefix.
func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
