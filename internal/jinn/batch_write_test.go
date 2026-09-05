package jinn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparedWriteToolLifecycle(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"multi_edit", "search_replace"} {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			for _, scenario := range []struct {
				name   string
				dryRun bool
				failAt int
			}{
				{name: "success"},
				{name: "preview", dryRun: true},
				{name: "first_commit_stale", failAt: 1},
				{name: "second_commit_stale", failAt: 2},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					t.Parallel()
					e, dir := testEngine(t)
					paths := []string{"a.txt", "b.txt", "c.txt"}
					for _, path := range paths {
						writeTestFile(t, dir, path, "alpha beta\n")
					}
					prepared := 0
					e.snapshotPreparedHook = func() {
						prepared++
						if prepared == scenario.failAt {
							writeTestFile(t, dir, paths[prepared-1], "external\n")
						}
					}
					result, _, err := e.Dispatch(t.Context(), tool, preparedWriteToolArgs(tool, paths, scenario.dryRun))
					if scenario.failAt != 0 {
						requireStaleFileError(t, err)
						if scenario.failAt == 1 && strings.Contains(err.Error(), "partial apply") {
							t.Fatalf("first failure reported a partial commit: %v", err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
					wantPrepared := len(paths)
					if scenario.dryRun {
						wantPrepared = 0
					} else if scenario.failAt != 0 {
						wantPrepared = scenario.failAt
					}
					if prepared != wantPrepared {
						t.Fatalf("prepared %d snapshots, want %d", prepared, wantPrepared)
					}
					for i, path := range paths {
						want := "new new\n"
						if scenario.dryRun || (scenario.failAt != 0 && i+1 > scenario.failAt) {
							want = "alpha beta\n"
						} else if i+1 == scenario.failAt {
							want = "external\n"
						}
						assertPreparedWriteContent(t, filepath.Join(dir, path), want)
					}
					history, historyErr := e.loadHistoryLocked()
					if historyErr != nil {
						t.Fatal(historyErr)
					}
					if len(history.Entries) != wantPrepared {
						t.Fatalf("history has %d entries, want %d", len(history.Entries), wantPrepared)
					}
					for i, entry := range history.Entries {
						wantState := historyStateCommitted
						if i+1 == scenario.failAt {
							wantState = historyStateAborted
						}
						if entry.DisplayPath != paths[i] || entry.Op != tool || entry.State != wantState {
							t.Fatalf("unexpected history entry: %+v", entry)
						}
						before, readErr := e.readBlob(entry)
						if readErr != nil || string(before) != "alpha beta\n" {
							t.Fatalf("snapshot before-content = %q, error = %v", before, readErr)
						}
					}
					if scenario.failAt == 2 {
						total := len(paths)
						if tool == "multi_edit" {
							total *= 2 // The existing error counts input edits, before grouping by file.
						}
						prefix := fmt.Sprintf("%s: partial apply — 1 of %d files already written: a.txt (undo id=%s); b.txt: ", tool, total, history.Entries[0].ID)
						if !strings.HasPrefix(err.Error(), prefix) || strings.Contains(err.Error(), history.Entries[1].ID) {
							t.Fatalf("partial receipt = %v, want prefix %q and no failed undo ID", err, prefix)
						}
					}
					if scenario.failAt == 0 {
						wantPrefix := "applied 6 edits:"
						if tool == "search_replace" {
							wantPrefix = "replaced 6 matches across 3 files:"
						}
						if scenario.dryRun {
							wantPrefix = "[dry-run]"
						}
						if result == nil || !strings.HasPrefix(result.Text, wantPrefix) {
							t.Fatalf("result = %+v, want prefix %q", result, wantPrefix)
						}
					}
				})
			}
		})
	}
}

func TestMultiEditContextReadPrecedesNextCommit(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	paths := []string{"a.txt", "b.txt"}
	for _, path := range paths {
		writeTestFile(t, dir, path, "alpha beta\n")
	}
	prepared := 0
	e.snapshotPreparedHook = func() {
		prepared++
		if prepared == 2 {
			writeTestFile(t, dir, paths[0], "later external change\n")
		}
	}
	request := preparedWriteToolArgs("multi_edit", paths, false)
	for _, edit := range request["edits"].([]interface{}) {
		edit.(map[string]interface{})["show_context"] = float64(1)
	}
	result, _, err := e.Dispatch(t.Context(), "multi_edit", request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "--- context ---") || !strings.Contains(result.Text, "new new") || strings.Contains(result.Text, "later external change") {
		t.Fatalf("context was not captured before the next commit: %s", result.Text)
	}
	assertPreparedWriteContent(t, filepath.Join(dir, paths[0]), "later external change\n")
}

func preparedWriteToolArgs(tool string, paths []string, dryRun bool) map[string]interface{} {
	checksum := sha256Hex([]byte("alpha beta\n"))
	if tool == "multi_edit" {
		var edits []interface{}
		for _, path := range paths {
			for _, old := range []string{"alpha", "beta"} {
				edits = append(edits, args("path", path, "old_text", old, "new_text", "new", "if_checksum", checksum))
			}
		}
		return args("edits", edits, "dry_run", dryRun)
	}
	var files []interface{}
	checksums := make(map[string]interface{}, len(paths))
	for _, path := range paths {
		files = append(files, path)
		checksums[path] = checksum
	}
	return args("pattern", "alpha|beta", "replacement", "new", "files", files, "if_checksums", checksums, "dry_run", dryRun)
}

func assertPreparedWriteContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("%s = %q, want %q; error = %v", filepath.Base(path), data, want, err)
	}
}
