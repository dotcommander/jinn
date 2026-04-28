package jinn

import (
	"fmt"
	"strings"
)

// patchOperation represents a single parsed operation from a Codex-style patch.
type patchOperation struct {
	kind     string        // "add", "delete", "update"
	path     string        // file path (relative or absolute)
	contents string        // for "add"
	chunks   []updateChunk // for "update"
}

// updateChunk represents a single hunk within an update operation.
type updateChunk struct {
	context  string   // optional @@ context line (after "@@ ")
	oldLines []string // lines prefixed with ' ' or '-'
	newLines []string // lines prefixed with ' ' or '+'
	isEOF    bool     // *** End of File marker
}

// parsePatch parses a Codex-style patch string into operations.
//
// Format:
//
//	*** Begin Patch
//	*** Add File: path
//	+line1
//	+line2
//	*** Delete File: path
//	*** Update File: path
//	@@ optional context
//	 unchanged
//	-removed
//	+added
//	*** End Patch
func parsePatch(text string) ([]patchOperation, error) {
	lines := strings.Split(normalizeToLF(strings.TrimSpace(text)), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("patch is empty or invalid")
	}
	if strings.TrimSpace(lines[0]) != "*** Begin Patch" {
		return nil, fmt.Errorf("the first line of the patch must be '*** Begin Patch'")
	}
	if strings.TrimSpace(lines[len(lines)-1]) != "*** End Patch" {
		return nil, fmt.Errorf("the last line of the patch must be '*** End Patch'")
	}

	var ops []patchOperation
	i := 1
	lastContent := len(lines) - 2

	for i <= lastContent {
		if strings.TrimSpace(lines[i]) == "" {
			i++
			continue
		}

		line := strings.TrimSpace(lines[i])

		if strings.HasPrefix(line, "*** Add File: ") {
			path := line[len("*** Add File: "):]
			i++
			var contentLines []string
			for i <= lastContent {
				next := lines[i]
				if strings.HasPrefix(strings.TrimSpace(next), "*** ") {
					break
				}
				if len(next) == 0 || next[0] != '+' {
					return nil, fmt.Errorf("invalid add-file line %q. Add file lines must start with '+'", next)
				}
				contentLines = append(contentLines, next[1:])
				i++
			}
			content := ""
			if len(contentLines) > 0 {
				content = strings.Join(contentLines, "\n") + "\n"
			}
			ops = append(ops, patchOperation{kind: "add", path: path, contents: content})
			continue
		}

		if strings.HasPrefix(line, "*** Delete File: ") {
			path := line[len("*** Delete File: "):]
			ops = append(ops, patchOperation{kind: "delete", path: path})
			i++
			continue
		}

		if strings.HasPrefix(line, "*** Update File: ") {
			path := line[len("*** Update File: "):]
			i++

			if i <= lastContent && strings.HasPrefix(strings.TrimSpace(lines[i]), "*** Move to: ") {
				return nil, fmt.Errorf("patch move operations (*** Move to:) are not supported")
			}

			var chunks []updateChunk
			for i <= lastContent {
				if strings.TrimSpace(lines[i]) == "" {
					i++
					continue
				}
				if strings.HasPrefix(strings.TrimSpace(lines[i]), "*** ") {
					break
				}
				chunk, nextIdx, err := parseUpdateChunk(lines, i, lastContent, len(chunks) == 0)
				if err != nil {
					return nil, fmt.Errorf("update %s: %w", path, err)
				}
				chunks = append(chunks, chunk)
				i = nextIdx
			}

			if len(chunks) == 0 {
				return nil, fmt.Errorf("update file hunk for path %q is empty", path)
			}
			ops = append(ops, patchOperation{kind: "update", path: path, chunks: chunks})
			continue
		}

		return nil, fmt.Errorf("%q is not a valid hunk header. Valid headers: '*** Add File:', '*** Delete File:', '*** Update File:'", line)
	}

	if len(ops) == 0 {
		return nil, fmt.Errorf("patch contains no operations")
	}
	return ops, nil
}

// parseUpdateChunk parses a single hunk starting at lines[startIdx].
func parseUpdateChunk(lines []string, startIdx, lastContentLine int, allowMissingContext bool) (updateChunk, int, error) {
	i := startIdx
	var ctx string
	first := strings.TrimRight(lines[i], " \t")

	if first == "@@" {
		i++
	} else if strings.HasPrefix(first, "@@ ") {
		ctx = first[3:]
		i++
	} else if !allowMissingContext {
		return updateChunk{}, i, fmt.Errorf("expected update hunk to start with @@ context marker, got: %q", lines[i])
	}

	var oldLines, newLines []string
	parsed := 0
	isEOF := false

	for i <= lastContentLine {
		raw := lines[i]
		trimmed := strings.TrimRight(raw, " \t")

		if trimmed == "*** End of File" {
			if parsed == 0 {
				return updateChunk{}, i, fmt.Errorf("update hunk does not contain any lines")
			}
			isEOF = true
			i++
			break
		}

		if parsed > 0 && (strings.HasPrefix(trimmed, "@@") || strings.HasPrefix(trimmed, "*** ")) {
			break
		}

		if len(raw) == 0 {
			oldLines = append(oldLines, "")
			newLines = append(newLines, "")
			parsed++
			i++
			continue
		}

		marker := raw[0]
		body := raw[1:]
		switch marker {
		case ' ':
			oldLines = append(oldLines, body)
			newLines = append(newLines, body)
		case '-':
			oldLines = append(oldLines, body)
		case '+':
			newLines = append(newLines, body)
		default:
			if parsed == 0 {
				return updateChunk{}, i, fmt.Errorf("unexpected line in update hunk: %q. Every line should start with ' ', '+', or '-'", raw)
			}
			goto done
		}
		parsed++
		i++
	}

done:
	if parsed == 0 {
		return updateChunk{}, i, fmt.Errorf("update hunk does not contain any lines")
	}

	return updateChunk{context: ctx, oldLines: oldLines, newLines: newLines, isEOF: isEOF}, i, nil
}

// seekSequence finds the index in lines where pattern matches sequentially,
// starting from start. If eof is true, searches from the end of the file.
// Uses progressive fuzzy matching: exact → rstrip → trim → Unicode-normalized.
func seekSequence(lines, pattern []string, start int, eof bool) int {
	if len(pattern) == 0 {
		return start
	}
	if len(pattern) > len(lines) {
		return -1
	}

	searchStart := start
	if eof && len(lines) >= len(pattern) {
		searchStart = len(lines) - len(pattern)
	}
	searchEnd := len(lines) - len(pattern)

	type eqFunc func(a, b string) bool
	passes := []eqFunc{
		func(a, b string) bool { return a == b },
		func(a, b string) bool { return strings.TrimRight(a, " \t") == strings.TrimRight(b, " \t") },
		func(a, b string) bool { return strings.TrimSpace(a) == strings.TrimSpace(b) },
		func(a, b string) bool { return normalizeForFuzzyMatch(a) == normalizeForFuzzyMatch(b) },
	}

	for _, eq := range passes {
		for i := searchStart; i <= searchEnd; i++ {
			ok := true
			for p := 0; p < len(pattern); p++ {
				if !eq(lines[i+p], pattern[p]) {
					ok = false
					break
				}
			}
			if ok {
				return i
			}
		}
	}
	return -1
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

	type replacement struct {
		start  int
		oldLen int
		newSeg []string
	}
	var replacements []replacement
	lineIndex := 0

	for _, chunk := range chunks {
		if chunk.context != "" {
			ctxIdx := seekSequence(lines, []string{chunk.context}, lineIndex, false)
			if ctxIdx < 0 {
				return "", fmt.Errorf("failed to find context %q in %s", chunk.context, filePath)
			}
			lineIndex = ctxIdx
		}

		if len(chunk.oldLines) == 0 {
			replacements = append(replacements, replacement{len(lines), 0, chunk.newLines})
			continue
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
			return "", fmt.Errorf("failed to find expected lines in %s:\n%s", filePath, strings.Join(chunk.oldLines, "\n"))
		}

		replacements = append(replacements, replacement{found, len(pattern), newSlice})
		lineIndex = found + len(pattern)
	}

	// Apply replacements in reverse order to preserve indices.
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

	return bom + strings.Join(result, "\n"), nil
}
