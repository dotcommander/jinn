package jinn

import (
	"fmt"
	"sort"
	"strings"
)

// detectOverlaps checks that no two edits for the same file target overlapping
// byte ranges in the original content. It also reorders rawEntries so that
// same-file edits are processed in top-to-bottom (positional) order; chained
// edits not found in the original retain their relative order after positioned
// edits. Returns the (possibly reordered) entries and any overlap error.
func detectOverlaps(rawEntries []rawEntry, originalContent map[string]string) ([]rawEntry, error) {
	fileOffsets := computeFileOffsets(rawEntries, originalContent)
	editPair := buildEditPairLookup(rawEntries)
	for _, entries := range fileOffsets {
		sortOffsetsAscending(entries)
		if err := checkFileOverlaps(entries, editPair); err != nil {
			return nil, err
		}
	}
	sortRawEntriesPositional(rawEntries, fileOffsets)
	return rawEntries, nil
}

// offsetEntry pairs an edit's index with where (and how long) its old_text
// matched in the original file content.
type offsetEntry struct {
	editIdx     int
	matchOffset int
	matchLength int
}

// computeFileOffsets locates each edit's old_text within its file's original
// content (falling back to fuzzy matching) and groups the located offsets by
// file. Edits whose old_text is not found in the original are omitted — they
// exist only in accumulated (chained) content and are skipped for overlap checks.
func computeFileOffsets(rawEntries []rawEntry, originalContent map[string]string) map[string][]offsetEntry {
	fileOffsets := make(map[string][]offsetEntry)
	for _, re := range rawEntries {
		origNorm := originalContent[re.resolved]
		oldNorm := normalizeToLF(re.oldText)
		offset := strings.Index(origNorm, oldNorm)
		if offset < 0 {
			origFuzzy := normalizeForFuzzyMatch(origNorm)
			oldFuzzy := normalizeForFuzzyMatch(oldNorm)
			offset = strings.Index(origFuzzy, oldFuzzy)
		}
		if offset >= 0 {
			fileOffsets[re.resolved] = append(fileOffsets[re.resolved], offsetEntry{re.idx, offset, len(oldNorm)})
		}
	}
	return fileOffsets
}

// buildEditPairLookup maps each edit index to its old_text+new_text signature
// for exact-duplicate detection.
func buildEditPairLookup(rawEntries []rawEntry) map[int]string {
	editPair := make(map[int]string)
	for _, re := range rawEntries {
		editPair[re.idx] = re.oldText + "\x00" + re.newText
	}
	return editPair
}

// sortOffsetsAscending sorts entries in place by match offset ascending.
func sortOffsetsAscending(entries []offsetEntry) {
	for a := 0; a < len(entries)-1; a++ {
		for b := a + 1; b < len(entries); b++ {
			if entries[a].matchOffset > entries[b].matchOffset {
				entries[a], entries[b] = entries[b], entries[a]
			}
		}
	}
}

// checkFileOverlaps reports an overlap error if any adjacent (offset-sorted)
// pair of edits target overlapping byte ranges. Exact-duplicate edits (same
// old_text→new_text) are skipped — they are handled by redundant edit skip in
// Phase 1b.
func checkFileOverlaps(entries []offsetEntry, editPair map[int]string) error {
	for k := 0; k < len(entries)-1; k++ {
		prev, curr := entries[k], entries[k+1]
		if editPair[prev.editIdx] == editPair[curr.editIdx] {
			continue
		}
		if prev.matchOffset+prev.matchLength > curr.matchOffset {
			i, j := prev.editIdx, curr.editIdx
			if i > j {
				i, j = j, i
			}
			return &ErrWithSuggestion{
				Err:        fmt.Errorf("edits[%d] and edits[%d] target overlapping regions", i, j),
				Suggestion: "split into separate multi_edit calls, or combine into a single edit covering the full region",
				Code:       ErrCodeEditOverlap,
			}
		}
	}
	return nil
}

// sortRawEntriesPositional reorders rawEntries so that same-file edits are
// processed in top-to-bottom order (by occurrence in original content). Edits
// whose old_text was not found in the original (chained/dependent edits) retain
// their original relative order and appear after positioned edits.
func sortRawEntriesPositional(rawEntries []rawEntry, fileOffsets map[string][]offsetEntry) {
	// Build a position map: editIdx -> matchOffset (MAX_INT if not in original).
	editPos := make(map[int]int)
	for _, oe := range fileOffsets {
		for _, entry := range oe {
			editPos[entry.editIdx] = entry.matchOffset
		}
	}
	const notFoundOffset = int(^uint(0) >> 1) // max int
	sort.SliceStable(rawEntries, func(a, b int) bool {
		reA, reB := rawEntries[a], rawEntries[b]
		if reA.resolved != reB.resolved {
			return false // different files: keep original order
		}
		posA, okA := editPos[reA.idx]
		posB, okB := editPos[reB.idx]
		if !okA {
			posA = notFoundOffset
		}
		if !okB {
			posB = notFoundOffset
		}
		return posA < posB
	})
}

// pairKey identifies an old_text→new_text replacement for redundant-edit detection.
type pairKey struct{ oldText, newText string }
