package jinn

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkSparseFindMetadata isolates the HEAD metadata-before-glob path and
// the current metadata-after-glob path on a local 1000-file tree (10 matches).
// It excludes Engine path policy, result JSON, pagination and external backends.
func BenchmarkSparseFindMetadata(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 1000; i++ {
		name := fmt.Sprintf("file_%04d.txt", i)
		if i%100 == 0 {
			name = fmt.Sprintf("file_%04d.go", i)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	for _, variant := range []struct {
		name            string
		metadataOnEvery bool
	}{
		{name: "HEAD_info_before_glob", metadataOnEvery: true},
		{name: "current_info_after_glob", metadataOnEvery: false},
	} {
		b.Run(variant.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				count, err := sparseFindWalk(root, variant.metadataOnEvery)
				if err != nil {
					b.Fatal(err)
				}
				if count != 10 {
					b.Fatalf("got %d matches, want 10", count)
				}
			}
		})
	}
}

func sparseFindWalk(root string, metadataOnEvery bool) (int, error) {
	matches := 0
	err := fs.WalkDir(os.DirFS(root), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." || entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		regular := entry.Type().IsRegular()
		if metadataOnEvery || !regular {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
		}
		if !globMatch("*.go", filepath.Base(path)) {
			return nil
		}
		if !metadataOnEvery && regular {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
		}
		matches++
		return nil
	})
	return matches, err
}
