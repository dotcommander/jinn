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

// deriveUpdatedContent applies update chunks to the current file content,
// producing the new content. Returns the updated content with BOM preserved.
func deriveUpdatedContent(filePath string, content string, chunks []updateChunk) (string, error) {
	raw, bom := stripBom(content)
	records := splitLineRecords(raw)
	lines := make([]string, len(records))
	for i := range records {
		lines[i] = records[i].text
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

	defaultEnding := detectLineEnding(raw)
	for i := len(replacements) - 1; i >= 0; i-- {
		r := replacements[i]
		ending := defaultEnding
		lastEnding := ""
		if r.start < len(records) && records[r.start].ending != "" {
			ending = records[r.start].ending
		}
		if r.oldLen > 0 && r.start+r.oldLen-1 < len(records) {
			lastEnding = records[r.start+r.oldLen-1].ending
		}
		newRecords := make([]lineRecord, len(r.newSeg))
		for index, text := range r.newSeg {
			newRecords[index] = lineRecord{text: text, ending: ending}
		}
		if len(newRecords) > 0 {
			switch {
			case lastEnding != "":
				newRecords[len(newRecords)-1].ending = lastEnding
			case r.start+r.oldLen < len(records):
				newRecords[len(newRecords)-1].ending = ending
			default:
				newRecords[len(newRecords)-1].ending = "\n"
			}
		}
		tail := append([]lineRecord(nil), records[r.start+r.oldLen:]...)
		records = append(records[:r.start], newRecords...)
		records = append(records, tail...)
	}
	var result strings.Builder
	result.WriteString(bom)
	for _, record := range records {
		result.WriteString(record.text)
		result.WriteString(record.ending)
	}
	return result.String(), nil
}

type lineRecord struct {
	text   string
	ending string
}

func splitLineRecords(raw string) []lineRecord {
	records := make([]lineRecord, 0, strings.Count(raw, "\n")+1)
	for start := 0; start < len(raw); {
		end := start
		for end < len(raw) && raw[end] != '\n' && raw[end] != '\r' {
			end++
		}
		ending := ""
		next := end
		if end < len(raw) {
			ending = raw[end : end+1]
			next = end + 1
			if raw[end] == '\r' && next < len(raw) && raw[next] == '\n' {
				ending = "\r\n"
				next++
			}
		}
		records = append(records, lineRecord{text: raw[start:end], ending: ending})
		start = next
	}
	if len(raw) == 0 {
		return nil
	}
	return records
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
