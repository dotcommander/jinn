package jinn

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	maxFileSize      = 50 << 20  // 50 MB absolute file limit
	readDefaultLines = 2000      // default window when no start_line/end_line given
	readMaxBytes     = 50 * 1024 // 50 KB output cap per chunk
	readTruncLines   = 2000      // head+tail collapse threshold
)

func (e *Engine) readFile(args map[string]interface{}) (*ToolResult, error) {
	path, _ := args["path"].(string)
	resolved, err := e.checkPath(path)
	if err != nil {
		// Wrap with "blocked:" prefix for backward compat, preserving any
		// ErrWithSuggestion so callers can surface the suggestion field.
		var sErr *ErrWithSuggestion
		if errors.As(err, &sErr) {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("blocked: %w", sErr.Err),
				Suggestion: sErr.Suggestion,
			}
		}
		return nil, fmt.Errorf("blocked: %w", err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("file not found: %s", path),
				Suggestion: "verify the path exists with list_dir on the parent, or check for typos",
			}
		}
		if os.IsPermission(err) {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("permission denied: %s", path),
				Suggestion: "file is not readable by the sandbox; check ownership or choose a different file",
			}
		}
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("not a regular file: %s", path),
			Suggestion: "target a regular file, not a directory — use list_dir to enumerate entries",
		}
	}
	if info.Size() > maxFileSize {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("file too large: %d MB (max 50 MB)", info.Size()>>20),
			Suggestion: "file is too large to read in one shot; use start_line/end_line to window, or search_files for a pattern",
		}
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsPermission(err) {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("permission denied: %s", path),
				Suggestion: "file is not readable by the sandbox; check ownership or choose a different file",
			}
		}
		return nil, err
	}

	e.tracker.record(resolved, info.ModTime())

	// PDF: reject before image/binary checks — pdftotext is a better tool.
	ext := strings.ToLower(filepath.Ext(resolved))
	if ext == ".pdf" {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("pdf extraction not supported in zero-dep mode"),
			Suggestion: "convert the PDF to text first (pdftotext, pdftk, or a cloud OCR service) and read the text file",
		}
	}

	// Image: return structured content block with base64 data and MIME type.
	if isImageExt(ext) {
		encoded := base64.StdEncoding.EncodeToString(data)
		mime := mimeTypeForExt(ext)
		return &ToolResult{
			Content: []ContentBlock{{
				Type:     "image",
				Data:     encoded,
				MimeType: mime,
			}},
		}, nil
	}

	check := data
	if len(check) > 512 {
		check = check[:512]
	}
	// Binary detection: return a plain result (not an error) with a suggestion
	// appended for LLM guidance — preserves backward compatibility.
	if strings.ContainsRune(string(check), 0) {
		return textResult(fmt.Sprintf("[binary file: %d bytes — use checksum_tree for integrity or skip content reads]", len(data))), nil
	}

	tail := 0
	if t, ok := args["tail"].(float64); ok && int(t) > 0 {
		tail = int(t)
	}

	// Parse truncate strategy early — "tail" mode shifts the default window to
	// the end of the file so the truncation function sees the final lines.
	truncateMode, _ := args["truncate"].(string)
	if truncateMode == "" {
		truncateMode = "head"
	}
	switch truncateMode {
	case "head", "tail", "middle", "none":
		// valid
	default:
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("invalid truncate value %q", truncateMode),
			Suggestion: `valid values are "head" (default), "tail", "middle", "none"`,
		}
	}

	lines := strings.Split(string(data), "\n")
	total := len(lines)
	if lines[total-1] == "" {
		total--
	}

	// Explicit tail= arg (number of lines from end) takes precedence.
	startLine := 1
	endLine := startLine + readDefaultLines - 1
	if tail > 0 {
		startLine = total - tail + 1
		if startLine < 1 {
			startLine = 1
		}
		endLine = total
	} else {
		// When truncate="tail" and no explicit window, pin window to end so
		// truncateOutputTail receives the last readDefaultLines lines.
		callerSetWindow := false
		if s, ok := args["start_line"].(float64); ok && int(s) >= 1 {
			startLine = int(s)
			callerSetWindow = true
		}
		if el, ok := args["end_line"].(float64); ok && int(el) >= startLine {
			endLine = int(el)
			callerSetWindow = true
		} else {
			endLine = startLine + readDefaultLines - 1
		}
		if truncateMode == "tail" && !callerSetWindow && total > readDefaultLines {
			startLine = total - readDefaultLines + 1
			endLine = total
		}
	}

	if startLine > total {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("file has %d lines, start_line %d is past end", total, startLine),
			Suggestion: fmt.Sprintf("requested window starts beyond file length (%d lines); reduce start_line", total),
		}
	}
	if endLine > total {
		endLine = total
	}

	lineNumbers := true
	if v, ok := args["line_numbers"].(bool); ok {
		lineNumbers = v
	}

	width := len(strconv.Itoa(endLine))
	var b strings.Builder
	for i := startLine - 1; i < endLine && i < len(lines); i++ {
		if lineNumbers {
			fmt.Fprintf(&b, "%*d\t%s\n", width, i+1, lines[i])
		} else {
			fmt.Fprintf(&b, "%s\n", lines[i])
		}
	}

	// Single oversized line guard: if the first source line exceeds the byte cap,
	// the byte-cap loop below would keep nothing. Return a hint instead.
	if startLine <= total && len(lines[startLine-1]) > readMaxBytes {
		srcLineLen := len(lines[startLine-1])
		return textResult(fmt.Sprintf(
			"[Line %d is %d KB, exceeds 50 KB limit. Use run_shell: sed -n '%dp' %s | head -c 50000]",
			startLine, srcLineLen/1024, startLine, path,
		)), nil
	}

	// Apply truncation strategy if windowed chunk exceeds the line limit.
	rawContent := strings.TrimRight(b.String(), "\n")
	var tr struct {
		Content    string
		Truncated  bool
		TotalLines int
		ShownLines int
	}
	switch truncateMode {
	case "head":
		tr = truncateOutputHead(rawContent, readTruncLines)
	case "tail":
		tr = truncateOutputTail(rawContent, readTruncLines)
	case "middle":
		tr = truncateOutputDetailed(rawContent, readTruncLines)
	case "none":
		tr.Content = rawContent
		lines2 := strings.Split(rawContent, "\n")
		tr.TotalLines = len(lines2)
		tr.ShownLines = len(lines2)
	}

	// Apply byte-size truncation: if the numbered output exceeds 50KB,
	// keep the head portion that fits and write the full remainder to a
	// temp file so the agent can pick up where it left off.
	outputBytes := len(tr.Content)
	byteTruncated := outputBytes > readMaxBytes

	if byteTruncated {
		outLines := strings.Split(tr.Content, "\n")
		if len(outLines) > 0 && outLines[len(outLines)-1] == "" {
			outLines = outLines[:len(outLines)-1]
		}
		var kept []string
		keptBytes := 0
		for _, l := range outLines {
			extra := len(l) + 1 // line + newline
			if keptBytes+extra > readMaxBytes {
				break
			}
			kept = append(kept, l)
			keptBytes += extra
		}

		// Collect source lines beyond the kept output lines for the remainder.
		// Each output line starts with "<num>\t", so the number of kept output
		// lines maps directly to source lines consumed.
		remainingStart := startLine + len(kept)
		var srcRemainder []string
		for i := remainingStart - 1; i < total && i < len(lines); i++ {
			srcRemainder = append(srcRemainder, lines[i])
		}
		tmpPath, _ := writeTruncationRemainder(resolved, remainingStart, srcRemainder)

		hint := fmt.Sprintf("\n[output truncated at %d KB; showing %d-%d of %d lines. ",
			keptBytes/1024, startLine, startLine+len(kept)-1, total)
		if tmpPath != "" {
			hint += fmt.Sprintf("Remainder saved to %s]", tmpPath)
		} else {
			hint += fmt.Sprintf("Use start_line=%d to continue]", startLine+len(kept))
		}

		return &ToolResult{
			Text: strings.Join(kept, "\n") + "\n" + hint,
			Meta: map[string]any{
				"truncation": truncationInfo{
					Truncated:   true,
					TotalLines:  total,
					OutputLines: len(kept),
				},
			},
		}, nil
	}

	// Determine if the file itself is longer than the window requested.
	windowTruncated := total > endLine

	result := tr.Content
	if windowTruncated {
		// Write remainder to temp file for seamless continuation.
		tmpPath, _ := writeTruncationRemainder(resolved, endLine+1, lines[endLine:total])
		hint := fmt.Sprintf("\n[file has %d lines; showing %d-%d. ", total, startLine, endLine)
		if tmpPath != "" {
			hint += fmt.Sprintf("Remainder saved to %s]", tmpPath)
		} else {
			hint += fmt.Sprintf("Use start_line=%d to continue]", endLine+1)
		}
		result += hint
	}

	// Build truncation metadata for callers (pi TUI, LLM context).
	// Set when either the window didn't cover the whole file, or head+tail
	// collapse happened within the windowed chunk.
	if windowTruncated || tr.Truncated {
		totalShown := tr.ShownLines
		if !tr.Truncated {
			totalShown = endLine - startLine + 1
		}
		return &ToolResult{
			Text: result,
			Meta: map[string]any{
				"truncation": truncationInfo{
					Truncated:   true,
					TotalLines:  total,
					OutputLines: totalShown,
				},
			},
		}, nil
	}

	return textResult(result), nil
}

// mimeTypeForExt returns the MIME type for a given lowercase dot-prefixed extension.
func mimeTypeForExt(ext string) string {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".svg":
		return "image/svg+xml"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".png":
		return "image/png"
	default:
		return "image/" + strings.TrimPrefix(ext, ".")
	}
}

// isImageExt reports whether ext (lowercase, dot-prefixed) is a supported image type.
func isImageExt(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp":
		return true
	}
	return false
}

// writeTruncationRemainder writes the lines from startLine onward to a temp file
// and returns the temp file path. Lines are written with line numbers. The temp
// file is placed in the XDG cache dir to avoid polluting the project tree.
// Errors are swallowed — the temp file is best-effort; the agent always has the
// start_line continuation fallback.
func writeTruncationRemainder(srcPath string, startLine int, remainderLines []string) (string, error) {
	if len(remainderLines) == 0 {
		return "", nil
	}
	base := filepath.Base(srcPath)
	userCache, _ := os.UserCacheDir()
	if userCache == "" {
		userCache = os.TempDir()
	}
	cacheDir := filepath.Join(userCache, "jinn", "truncated")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	tmpFile, err := os.CreateTemp(cacheDir, base+".*.txt")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	endLine := startLine + len(remainderLines) - 1
	width := len(strconv.Itoa(endLine))
	for i, line := range remainderLines {
		fmt.Fprintf(tmpFile, "%*d\t%s\n", width, startLine+i, line)
	}

	return tmpFile.Name(), nil
}
