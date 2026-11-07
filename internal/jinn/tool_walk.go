package jinn

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// walkEntryHandler processes one non-pruned, non-root entry encountered by
// walkNativeDir. It receives the root-relative path (relBase), the walk path,
// and the entry. Returning fs.SkipAll or filepath.SkipDir stops the walk
// without an error, matching WalkDir semantics.
type walkEntryHandler func(relBase, path string, entry fs.DirEntry) error

// walkNativeDir is the shared traversal lifecycle for directory tools
// (list_dir, find_files). It owns the invariant walk orchestration:
// context cancellation, I/O error propagation, root skipping, hidden/excluded
// pruning, and the visit cap. Per-tool policy (pattern matching, depth,
// changedSince, symlinks, paging) stays in the caller's handler.
//
// visitCap bounds non-pruned entries; capHit reports a cap-triggered stop so
// callers can distinguish it from a natural or handler-triggered stop.
func walkNativeDir(ctx context.Context, fsys fs.FS, rootPath string, visitCap int, handle walkEntryHandler) (capHit bool, err error) {
	visited := 0
	err = fs.WalkDir(fsys, rootPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return walkErr
		}
		relBase := rootRelativeFast(rootPath, path)
		if relBase == "" {
			// Fast path missed (unusual root form): fall back to Rel.
			var relErr error
			relBase, relErr = filepath.Rel(rootPath, path)
			if relErr != nil {
				return relErr
			}
		}
		if relBase == "." {
			return nil
		}
		if shouldPruneTraversal(relBase, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		if visited > visitCap {
			capHit = true
			return fs.SkipAll
		}
		return handle(relBase, path, entry)
	})
	if err != nil {
		return capHit, err
	}
	return capHit, nil
}

// rootRelativeFast returns path relative to rootPath without allocating a
// fresh cleaned path, covering the forms fs.WalkDir actually produces.
// It returns "" when the fast path does not apply (caller falls back to
// filepath.Rel), and "." for the root itself.
func rootRelativeFast(rootPath, path string) string {
	if path == rootPath {
		return "."
	}
	if rootPath == "." {
		return path
	}
	prefix := rootPath + string(os.PathSeparator)
	if strings.HasPrefix(path, prefix) {
		return path[len(prefix):]
	}
	return ""
}
