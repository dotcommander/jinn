package jinn

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditC201(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, old, new string
		line           int
	}{
		{"first", "a\nb\n", "b\n", 1},
		{"middle", "a\nb\nc\n", "a\nc\n", 2},
		{"EOF", "a\nb\n", "a\n", 2},
		{"whole", "a\nb\n", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, context := range []int{0, 3} {
				got := generateEditDiff(tc.old, tc.new, "file", matchInfo{startLine: 1, endLine: 2}, tc.old, tc.new, context)
				if got.FirstChangedLine != tc.line {
					t.Fatalf("context %d: first line %d, want %d; %s", context, got.FirstChangedLine, tc.line, got.Diff)
				}
			}
		})
	}
}

func TestAuditC202(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, old, match, replacement string }{
		{"delete", "a\nb\nc\n", "a\n", ""},
		{"terminated", "a\nb\nc\n", "b\n", "B\n"},
		{"partial", "prefix old suffix\nnext\n", "old", "new"},
		{"EOF", "a\nb", "b", ""},
		{"whole", "a\n", "a\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			updated, _, info, err := applyEdit([]byte(tc.old), tc.match, tc.replacement, false)
			if err != nil {
				t.Fatal(err)
			}
			got := generateEditDiff(tc.old, updated, "file", info, tc.match, tc.replacement, 3)
			want := generateDiff(tc.old, updated, "file", 3)
			if got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestAuditC203(t *testing.T) {
	t.Parallel()
	original := "“a”   \r\nsecond\r\n“third”\r\n"
	entries := []rawEntry{
		{idx: 0, resolved: "file", oldText: "second", newText: "SECOND"},
		{idx: 1, resolved: "file", oldText: `"third"` + "\n", newText: "THIRD\n"},
	}
	ordered, err := detectOverlaps(entries, map[string]string{"file": original})
	if err != nil || ordered[0].idx != 0 {
		t.Fatalf("disjoint ranges: order %+v, error %v", ordered, err)
	}
	entries[0].oldText = "third”"
	if _, err := detectOverlaps(entries, map[string]string{"file": original}); err == nil {
		t.Fatal("expected overlap between exact and fuzzy original-byte intervals")
	}
}

func TestAuditC204(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	e.requireMutationPreconditions = true
	writeTestFile(t, dir, "a", "old\n")
	patch := "*** Begin Patch\n*** Update File: a\n@@\n-old\n+new\n*** End Patch"
	if _, err := e.applyPatch(args("patch", patch, "dry_run", true)); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a")); string(got) != "old\n" {
		t.Fatalf("dry run changed file: %q", got)
	}
	if history, err := e.loadHistoryLocked(); err != nil || len(history.Entries) != 0 {
		t.Fatalf("dry run history: %+v, %v", history, err)
	}
	if _, err := e.applyPatch(args("patch", patch)); err == nil {
		t.Fatal("mutation accepted missing checksum")
	}
	if _, err := e.applyPatch(args("patch", patch, "if_checksums", map[string]interface{}{"a": strings.Repeat("0", 64)})); err == nil {
		t.Fatal("mutation accepted stale checksum")
	}
}

func TestAuditC205(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, literal string }{
		{"char", "x := '}'"},
		{"escapedChar", `x := '\''; y := '}'`},
		{"quoted", `x := "\\\"}"`},
		{"raw", "x := `\n}\n`"},
		{"comment", "/*\n}\n*/"},
		{"rustRaw", "let x = r##\"\n}\n\"##;"},
		{"cppRaw", "auto x = R\"tag(\n}\n)tag\";"},
		{"javaText", "var x = \"\"\"\n}\n\"\"\";"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			lines := strings.Split("header\nfunc f() {\n"+tc.literal+"\n}\nafter", "\n")
			if got, ok := braceCutLine(lines, len(lines)-2); !ok || got != 1 {
				t.Fatalf("inside block cut = %d, %v", got, ok)
			}
			if got, ok := braceCutLine(lines, len(lines)-1); !ok || got != len(lines)-1 {
				t.Fatalf("after block cut = %d, %v", got, ok)
			}
		})
	}
	if _, ok := braceCutLine([]string{"func f() {", "body"}, 2); ok {
		t.Fatal("expected bounded fallback for unclosed block")
	}
}

func TestAuditC2I01(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ old, want string }{
		{"a\r\n", "a\r\nb\r\n"},
		{"a\n", "a\nb\n"},
		{"a", "a\nb\n"},
		{"", "b\n"},
	} {
		got, err := deriveUpdatedContent("file", tc.old, []updateChunk{{newLines: []string{"b"}}})
		if err != nil || got != tc.want {
			t.Fatalf("append %q = %q, %v; want %q", tc.old, got, err, tc.want)
		}
	}
}

func TestAuditC2I02(t *testing.T) {
	t.Parallel()
	err := partialApplyErr("patch", []appliedRef{
		{path: "a", resolved: "/root/a", undoID: "one"},
		{path: "./a", resolved: "/root/a", undoID: "two"},
	}, 2, errors.New("failed b"))
	for _, want := range []string{"1 of 2 files", "a (undo id=one)", "./a (undo id=two)"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q in %v", want, err)
		}
	}
}

func TestAuditC2R01(t *testing.T) {
	// Serial: the spill registry root is process-global environment state.
	t.Setenv("JINN_CONFIG_DIR", t.TempDir())
	lines := []string{"one", strings.Repeat("界", 10000), "three"}
	path, err := writeTruncationRemainder("source", 7, lines)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if !isRegisteredShellSpill(path) {
		t.Fatal("returned remainder is not immediately registered")
	}
	data, err := os.ReadFile(path)
	var want strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&want, "%d\t%s\n", 7+i, line)
	}
	if err != nil || string(data) != want.String() {
		t.Fatalf("incomplete remainder: %d bytes, error %v", len(data), err)
	}
}

func TestAuditC2R02(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body string }{
		{"independent", "*** Update File: a\n@@\n-one\n+ONE\n*** Update File: a\n@@\n-two\n+TWO\n"},
		{"dependent", "*** Update File: a\n@@\n-one\n+ONE\n*** Update File: a\n@@\n-ONE\n+final\n"},
		{"alias", "*** Update File: a\n@@\n-one\n+ONE\n*** Update File: ./a\n@@\n-two\n+TWO\n"},
		{"deleteAdd", "*** Delete File: a\n*** Add File: ./a\n+new\n"},
		{"addDelete", "*** Add File: missing\n+new\n*** Delete File: ./missing\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e, dir := testEngine(t)
			writeTestFile(t, dir, "a", "one\ntwo\n")
			for _, dryRun := range []bool{false, true} {
				_, err := e.applyPatch(args("patch", "*** Begin Patch\n"+tc.body+"*** End Patch", "dry_run", dryRun))
				var explained *ErrWithSuggestion
				if !errors.As(err, &explained) || explained.Code != ErrCodeInvalidArgs || !strings.Contains(err.Error(), "operations[0] and operations[1]") || !strings.Contains(explained.Suggestion, "ordered chunks") {
					t.Fatalf("dry_run=%v: expected corrective duplicate-target error, got %v", dryRun, err)
				}
				if got, _ := os.ReadFile(filepath.Join(dir, "a")); string(got) != "one\ntwo\n" {
					t.Fatalf("preflight mutated content %q", got)
				}
				if _, err := os.Stat(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
					t.Fatalf("preflight created missing target: %v", err)
				}
				if history, err := e.loadHistoryLocked(); err != nil || len(history.Entries) != 0 {
					t.Fatalf("preflight wrote history: %+v, %v", history, err)
				}
			}
		})
	}
}

func TestAuditC2R02OrderedChunks(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "a", "one\ntwo\nthree\n")
	patch := "*** Begin Patch\n*** Update File: a\n@@\n-one\n+ONE\n@@\n-three\n+THREE\n*** End Patch"
	if _, err := e.applyPatch(args("patch", patch)); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a")); string(got) != "ONE\ntwo\nTHREE\n" {
		t.Fatalf("ordered chunks produced %q", got)
	}
}
