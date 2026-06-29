package jinn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// lspLocation is shared by definition and references responses.
type lspLocation struct {
	URI   string `json:"uri"`
	Range struct {
		Start struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"start"`
	} `json:"range"`
}

func formatLocation(loc lspLocation) string {
	return fmt.Sprintf("%s:%d:%d", loc.URI, loc.Range.Start.Line+1, loc.Range.Start.Character+1)
}

// lspLocationLink is an alternative definition response format.
// Some servers return LocationLink[] instead of Location[].
type lspLocationLink struct {
	TargetURI            string   `json:"targetUri"`
	TargetRange          lspRange `json:"targetRange"`
	TargetSelectionRange lspRange `json:"targetSelectionRange"`
}

// lspRange is a reusable LSP range type shared by location links and edits.
type lspRange struct {
	Start struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"start"`
	End struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"end"`
}

// lspWorkspaceEdit is the result of textDocument/rename.
type lspWorkspaceEdit struct {
	Changes         map[string][]lspTextEdit `json:"changes,omitempty"`
	DocumentChanges []lspTextDocumentEdit    `json:"documentChanges,omitempty"`
}

// lspTextDocumentEdit is the text-edit subset of WorkspaceEdit.documentChanges.
// Resource operations are intentionally ignored by the preview formatter.
type lspTextDocumentEdit struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Edits []lspTextEdit `json:"edits"`
}

// lspTextEdit is a single text replacement in a document.
type lspTextEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

// lspDiagnostic is the subset of LSP Diagnostic fields Jinn renders.
type lspDiagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity,omitempty"`
	Code     any      `json:"code,omitempty"`
	Source   string   `json:"source,omitempty"`
	Message  string   `json:"message"`
}

// --- position helpers ---

// lspPosition converts 1-based line/char to 0-based LSP position.
func lspPosition(line, char int) map[string]any {
	return map[string]any{"line": line - 1, "character": char - 1}
}

func tdPos(absPath string, line, char int) map[string]any {
	return map[string]any{
		"textDocument": map[string]string{"uri": pathToURI(absPath)},
		"position":     lspPosition(line, char),
	}
}

// findSymbolColumn reads line (0-based) from absPath and returns the 0-based
// UTF-16 character offset of the first occurrence of symbol, matching the LSP
// Position.character contract.
func findSymbolColumn(absPath string, line int, symbol string) (int, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return 0, fmt.Errorf("find symbol column: %w", err)
	}
	lines := splitLSPLines(string(data))
	if line < 0 || line >= len(lines) {
		return 0, fmt.Errorf("line %d out of range (file has %d lines)", line+1, len(lines))
	}
	lineText := lines[line]
	before, _, ok := strings.Cut(lineText, symbol)
	if !ok {
		return 0, fmt.Errorf("symbol %q not found on line %d", symbol, line+1)
	}
	return utf16CodeUnitLen(before), nil
}

func splitLSPLines(source string) []string {
	return strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
}

func utf16CodeUnitLen(s string) int {
	return len(utf16.Encode([]rune(s)))
}

// lspCachedLines reads a file into a line slice, caching results for the
// lifetime of one query. Returns nil on read error (context is best-effort).
func lspCachedLines(cache map[string][]string, path string) []string {
	if lines, ok := cache[path]; ok {
		return lines
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	cache[path] = lines
	return lines
}

// lspFormatContext renders contextSize lines around targetLine (0-based) with
// a "> " marker on the target line. Returns empty string if lines is nil/empty.
func lspFormatContext(lines []string, targetLine, contextSize int) string {
	if len(lines) == 0 || contextSize <= 0 {
		return ""
	}
	start := targetLine - contextSize
	if start < 0 {
		start = 0
	}
	end := targetLine + contextSize + 1
	if end > len(lines) {
		end = len(lines)
	}

	var sb strings.Builder
	for i := start; i < end; i++ {
		marker := "  "
		if i == targetLine {
			marker = "> "
		}
		fmt.Fprintf(&sb, "%s%4d | %s\n", marker, i+1, lines[i])
	}
	return strings.TrimRight(sb.String(), "\n")
}

// formatWorkspaceEdit formats rename results as "file: N edit(s)" + per-edit lines.
// workDir is used to compute relative paths for readability.
func formatWorkspaceEdit(edit *lspWorkspaceEdit, workDir string) string {
	fileEdits := collectWorkspaceEditFileEdits(edit)
	if len(fileEdits) == 0 {
		return "no changes"
	}
	var sb strings.Builder
	totalEdits := 0
	for uri, edits := range fileEdits {
		path := strings.TrimPrefix(uri, "file://")
		rel := path
		if workDir != "" {
			if r, err := filepath.Rel(workDir, path); err == nil {
				rel = r
			}
		}
		fmt.Fprintf(&sb, "%s: %d edit(s)\n", rel, len(edits))
		for _, e := range edits {
			line := e.Range.Start.Line + 1
			fmt.Fprintf(&sb, "  line %d: %q\n", line, e.NewText)
		}
		totalEdits += len(edits)
	}
	fmt.Fprintf(&sb, "\n%d file(s), %d edit(s) total", len(fileEdits), totalEdits)
	return sb.String()
}

func collectWorkspaceEditFileEdits(edit *lspWorkspaceEdit) map[string][]lspTextEdit {
	if edit == nil {
		return nil
	}
	if len(edit.DocumentChanges) > 0 {
		fileEdits := make(map[string][]lspTextEdit)
		for _, change := range edit.DocumentChanges {
			uri := change.TextDocument.URI
			if uri == "" || len(change.Edits) == 0 {
				continue
			}
			fileEdits[uri] = append(fileEdits[uri], change.Edits...)
		}
		if len(fileEdits) > 0 {
			return fileEdits
		}
	}
	return edit.Changes
}
