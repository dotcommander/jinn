package jinn

import (
	"fmt"
	"strings"
)

// lineReplacement is a resolved edit: replace oldLen lines at start with newSeg.
type lineReplacement struct {
	start  int
	oldLen int
	newSeg []string
}

// resolveChunk locates a single update chunk within lines (advancing past any
// context marker) and returns the replacement plus the next search index.
func resolveChunk(lines []string, chunk updateChunk, lineIndex int, filePath string) (lineReplacement, int, error) {
	if chunk.context != "" {
		ctxIdx := seekSequence(lines, []string{chunk.context}, lineIndex, false)
		if ctxIdx < 0 {
			return lineReplacement{}, lineIndex, fmt.Errorf("failed to find context %q in %s", chunk.context, filePath)
		}
		lineIndex = ctxIdx
	}

	if len(chunk.oldLines) == 0 {
		return lineReplacement{len(lines), 0, chunk.newLines}, lineIndex, nil
	}

	pattern := chunk.oldLines
	newSlice := chunk.newLines

	found := seekSequence(lines, pattern, lineIndex, chunk.isEOF)
	if found < 0 && len(pattern) > 0 && pattern[len(pattern)-1] == "" {
		pattern = pattern[:len(pattern)-1]
		if len(newSlice) > 0 && newSlice[len(newSlice)-1] == "" {
			newSlice = newSlice[:len(newSlice)-1]
		}
		found = seekSequence(lines, pattern, lineIndex, chunk.isEOF)
	}

	if found < 0 {
		return lineReplacement{}, lineIndex, fmt.Errorf("failed to find expected lines in %s:\n%s", filePath, strings.Join(chunk.oldLines, "\n"))
	}

	return lineReplacement{found, len(pattern), newSlice}, found + len(pattern), nil
}

// applyReplacements applies replacements to lines in reverse order (to preserve
// indices) and ensures a trailing empty element for the final newline.
func applyReplacements(lines []string, replacements []lineReplacement) []string {
	result := make([]string, len(lines))
	copy(result, lines)
	for i := len(replacements) - 1; i >= 0; i-- {
		r := replacements[i]
		tail := result[r.start+r.oldLen:]
		result = append(result[:r.start], r.newSeg...)
		result = append(result, tail...)
	}

	// Ensure trailing newline.
	if len(result) == 0 || result[len(result)-1] != "" {
		result = append(result, "")
	}
	return result
}

// deriveUpdatedContent applies update chunks to the current file content,
// producing the new content. Returns the updated content with BOM preserved.
func deriveUpdatedContent(filePath string, content string, chunks []updateChunk) (string, error) {
	raw, bom := stripBom(content)
	raw = normalizeToLF(raw)

	lines := strings.Split(raw, "\n")
	// Remove trailing empty element from final newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" && strings.HasSuffix(raw, "\n") {
		lines = lines[:len(lines)-1]
	}

	var replacements []lineReplacement
	lineIndex := 0

	for _, chunk := range chunks {
		r, nextIndex, err := resolveChunk(lines, chunk, lineIndex, filePath)
		if err != nil {
			return "", err
		}
		replacements = append(replacements, r)
		lineIndex = nextIndex
	}

	result := applyReplacements(lines, replacements)
	return bom + strings.Join(result, "\n"), nil
}
