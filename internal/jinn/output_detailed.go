package jinn

import "strings"

// TruncationResult describes how output was truncated, mirroring pi's
// truncateHead/truncateTail result structure.
type TruncationResult struct {
	Truncated   bool   `json:"truncated"`
	TruncatedBy string `json:"truncatedBy,omitempty"` // "lines", "bytes", or ""
	TotalLines  int    `json:"totalLines"`
	TotalBytes  int    `json:"totalBytes"`
	OutputLines int    `json:"outputLines"`
	OutputBytes int    `json:"outputBytes"`
	MaxLines    int    `json:"maxLines"`
	MaxBytes    int    `json:"maxBytes"`
}

// splitLines splits s into lines without a trailing empty element.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// truncateHeadDetailed truncates output keeping the first N lines/bytes,
// whichever limit is hit first. Returns both the content and structured
// metadata about the truncation. Suitable for file lists where the
// beginning matters (alphabetical order).
func truncateHeadDetailed(raw string, maxLines, maxBytes int) (string, TruncationResult) {
	totalBytes := len(raw)
	lines := splitLines(raw)
	totalLines := len(lines)

	result := TruncationResult{
		TotalLines: totalLines,
		TotalBytes: totalBytes,
		MaxLines:   maxLines,
		MaxBytes:   maxBytes,
	}

	if totalLines <= maxLines && totalBytes <= maxBytes {
		result.OutputLines = totalLines
		result.OutputBytes = totalBytes
		return raw, result
	}

	// Collect complete lines from the start that fit both limits.
	var kept []string
	keptBytes := 0
	truncatedBy := "lines"

	for i := 0; i < len(lines) && len(kept) < maxLines; i++ {
		lineBytes := len(lines[i])
		if len(kept) > 0 {
			lineBytes++ // newline separator
		}
		if keptBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		kept = append(kept, lines[i])
		keptBytes += lineBytes
	}

	if len(kept) >= maxLines && keptBytes <= maxBytes {
		truncatedBy = "lines"
	}

	content := strings.Join(kept, "\n")
	result.Truncated = true
	result.TruncatedBy = truncatedBy
	result.OutputLines = len(kept)
	result.OutputBytes = len(content)
	return content, result
}

// truncateTailDetailed truncates output keeping the last N lines/bytes,
// whichever limit is hit first. Returns both the content and structured
// metadata about the truncation.
func truncateTailDetailed(raw string, maxLines, maxBytes int) (string, TruncationResult) {
	totalBytes := len(raw)
	lines := splitLines(raw)
	totalLines := len(lines)

	result := TruncationResult{
		TotalLines: totalLines,
		TotalBytes: totalBytes,
		MaxLines:   maxLines,
		MaxBytes:   maxBytes,
	}

	if totalLines <= maxLines && totalBytes <= maxBytes {
		result.OutputLines = totalLines
		result.OutputBytes = totalBytes
		return raw, result
	}

	// Work backwards from the end, collecting lines that fit both limits.
	var kept []string
	keptBytes := 0
	truncatedBy := "lines"

	for i := totalLines - 1; i >= 0 && len(kept) < maxLines; i-- {
		lineBytes := len(lines[i])
		if len(kept) > 0 {
			lineBytes++ // newline separator
		}
		if keptBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		kept = append(kept, "") // extend
		copy(kept[1:], kept)
		kept[0] = lines[i]
		keptBytes += lineBytes
	}

	// If we hit the line limit within byte budget, truncatedBy is "lines".
	if len(kept) >= maxLines && keptBytes <= maxBytes {
		truncatedBy = "lines"
	}

	content := strings.Join(kept, "\n")
	result.Truncated = true
	result.TruncatedBy = truncatedBy
	result.OutputLines = len(kept)
	result.OutputBytes = len(content)
	return content, result
}
