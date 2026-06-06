package jinn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemovedToolReferencesStayHistorical(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	removedRefs := []string{
		"checksum" + "_tree",
		"related" + "_context",
		"tool_" + "checksum.go",
		"tool_" + "related" + "_context.go",
		"related" + "-context-test",
	}
	allowed := map[string]bool{
		"CHANGELOG.md": true,
	}

	var hits []string
	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".dc", ".git", ".work":
				return filepath.SkipDir
			}
			return nil
		}
		if name == "jinn" {
			return nil
		}

		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if allowed[rel] {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.ContainsRune(string(data), '\x00') {
			return nil
		}
		content := string(data)
		for _, ref := range removedRefs {
			if strings.Contains(content, ref) || strings.Contains(rel, ref) {
				hits = append(hits, rel+" contains "+ref)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan repo for removed tool references: %v", err)
	}
	if len(hits) > 0 {
		t.Fatalf("removed tool references must stay confined to CHANGELOG history:\n%s", strings.Join(hits, "\n"))
	}
}
