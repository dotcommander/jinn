package jinn

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func walkTestFS(t *testing.T) fs.FS {
	t.Helper()
	root := t.TempDir()
	files := []string{"a.txt", "b.txt", "sub/c.txt", "sub/d.txt", ".hidden/e.txt"}
	for _, name := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return os.DirFS(root)
}

func TestWalkNativeDir_Composition(t *testing.T) {
	t.Parallel()
	var seen []string
	capHit, err := walkNativeDir(context.Background(), walkTestFS(t), ".", 100, func(relBase, path string, entry fs.DirEntry) error {
		if !entry.IsDir() {
			seen = append(seen, relBase)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if capHit {
		t.Fatal("capHit = true, want false (4 visible files under cap 100)")
	}
	if len(seen) != 4 {
		t.Fatalf("seen = %v, want 4 non-pruned entries (hidden dir pruned)", seen)
	}
}

func TestWalkNativeDir_VisitCap(t *testing.T) {
	t.Parallel()
	handled := 0
	capHit, err := walkNativeDir(context.Background(), walkTestFS(t), ".", 2, func(relBase, path string, entry fs.DirEntry) error {
		handled++
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if !capHit {
		t.Fatal("capHit = false, want true (4 visible files over cap 2)")
	}
	if handled != 2 {
		t.Fatalf("handled = %d, want 2 (cap stops before third entry)", handled)
	}
}

func TestWalkNativeDir_HandlerSkipAll(t *testing.T) {
	t.Parallel()
	handled := 0
	capHit, err := walkNativeDir(context.Background(), walkTestFS(t), ".", 100, func(relBase, path string, entry fs.DirEntry) error {
		handled++
		return fs.SkipAll
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if capHit {
		t.Fatal("capHit = true, want false (handler SkipAll is not a cap hit)")
	}
	if handled != 1 {
		t.Fatalf("handled = %d, want 1 (SkipAll stops the walk)", handled)
	}
}

func TestWalkNativeDir_CancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := walkNativeDir(ctx, walkTestFS(t), ".", 100, func(relBase, path string, entry fs.DirEntry) error {
		t.Fatal("handler must not run with a cancelled context")
		return nil
	})
	if err == nil {
		t.Fatal("err = nil, want context cancellation")
	}
}
