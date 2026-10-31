package jinn

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFindFilesFiltersNamesBeforeRegularFileMetadata(t *testing.T) {
	t.Parallel()
	e, dir := testEngine(t)
	writeTestFile(t, dir, "match.go", "package match")
	writeTestFile(t, dir, "other.txt", "other")
	writeTestFile(t, dir, filepath.Join("sub", "nested.go"), "package nested")
	if err := os.Symlink("match.go", filepath.Join(dir, "alias.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	result, err := e.findFiles(context.Background(), args("pattern", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := parseFindResult(t, result)
	if got.TotalCount != 2 || len(got.Files) != 2 || got.Files[0] != "match.go" || got.Files[1] != "sub/nested.go" || !got.TotalCountExact {
		t.Fatalf("unexpected find result: %+v", got)
	}
}
