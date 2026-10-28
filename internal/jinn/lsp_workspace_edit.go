package jinn

import (
	"fmt"
	"path/filepath"
	"strings"
)

func workspaceEditPath(uri, workDir string) string {
	path, err := fileURIPath(uri)
	if err != nil {
		return uri
	}
	if workDir != "" {
		if rel, err := filepath.Rel(workDir, path); err == nil {
			return rel
		}
	}
	return path
}

func formatOrderedWorkspaceEdit(edit *lspWorkspaceEdit, workDir string) string {
	var output strings.Builder
	files := make(map[string]bool)
	textEdits, resources := 0, 0
	for _, change := range edit.DocumentChanges {
		switch change.Kind {
		case "create", "delete":
			fmt.Fprintf(&output, "%s %s\n", change.Kind, workspaceEditPath(change.URI, workDir))
			files[change.URI] = true
			resources++
		case "rename":
			fmt.Fprintf(&output, "rename %s -> %s\n", workspaceEditPath(change.OldURI, workDir), workspaceEditPath(change.NewURI, workDir))
			files[change.OldURI], files[change.NewURI] = true, true
			resources++
		default:
			uri := change.TextDocument.URI
			if uri == "" || len(change.Edits) == 0 {
				continue
			}
			fmt.Fprintf(&output, "%s: %d edit(s)\n", workspaceEditPath(uri, workDir), len(change.Edits))
			for _, edit := range change.Edits {
				fmt.Fprintf(&output, "  line %d: %q\n", edit.Range.Start.Line+1, edit.NewText)
			}
			files[uri] = true
			textEdits += len(change.Edits)
		}
	}
	if output.Len() == 0 {
		return "no changes"
	}
	fmt.Fprintf(&output, "\n%d file(s), %d edit(s) total", len(files), textEdits)
	if resources > 0 {
		fmt.Fprintf(&output, ", %d resource operation(s)", resources)
	}
	return output.String()
}

func workspaceEditURIs(edit *lspWorkspaceEdit) map[string]bool {
	result := make(map[string]bool)
	for uri := range collectWorkspaceEditFileEdits(edit) {
		result[uri] = true
	}
	for _, change := range edit.DocumentChanges {
		switch change.Kind {
		case "create", "delete":
			result[change.URI] = true
		case "rename":
			result[change.OldURI], result[change.NewURI] = true, true
		}
	}
	return result
}
