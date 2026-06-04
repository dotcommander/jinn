package jinn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	Changes map[string][]lspTextEdit `json:"changes,omitempty"`
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
// character offset of the first occurrence of symbol. The offset is in runes
// (UTF-16 code units are close enough for BMP; jinn targets ASCII-heavy source).
func findSymbolColumn(absPath string, line int, symbol string) (int, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return 0, fmt.Errorf("find symbol column: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	if line < 0 || line >= len(lines) {
		return 0, fmt.Errorf("line %d out of range (file has %d lines)", line+1, len(lines))
	}
	lineText := lines[line]
	before, _, ok := strings.Cut(lineText, symbol)
	if !ok {
		return 0, fmt.Errorf("symbol %q not found on line %d", symbol, line+1)
	}
	return len([]rune(before)), nil
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
	if edit == nil || len(edit.Changes) == 0 {
		return "no changes"
	}
	var sb strings.Builder
	totalEdits := 0
	for uri, edits := range edit.Changes {
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
	fmt.Fprintf(&sb, "\n%d file(s), %d edit(s) total", len(edit.Changes), totalEdits)
	return sb.String()
}
