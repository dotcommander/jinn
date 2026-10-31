package jinn

import (
	"fmt"
	"os"
)

const (
	maxFileSize      = 50 << 20  // 50 MB absolute file limit
	readDefaultLines = 2000      // default read window when no start_line/end_line given; distinct knob from readTruncLines (window size, not collapse point). tunable: config candidate
	readMaxBytes     = 50 * 1024 // 50 KB output cap per chunk. tunable: config candidate
	readTruncLines   = 2000      // head+tail collapse threshold; distinct knob from readDefaultLines (when to collapse output, not how much to read)
)

// readContentResult holds the output of readFileContent.
// The caller is responsible for wrapping this into a ToolResult.
type readContentResult struct {
	Content     string // processed, line-numbered (and possibly truncated) text
	TotalLines  int    // total lines in the source file
	OutputLines int    // lines actually included in Content
	Truncated   bool   // true if content was truncated in any way
	ByteHint    string // truncation hint appended after Content (byte or window)
	TempFile    string // path to spilled remainder file, if any
	NextLine    int    // exact next 1-based source line, when sequential continuation is safe
	Checksum    string // SHA-256 checksum of full file bytes when requested
}

// readFileContent reads and processes a file's text content. It handles stat
// checks, reading, PDF/binary detection, line splitting, windowing, and
// truncation. The caller is responsible for sandbox validation (checkPath),
// image detection, checksum computation, and ToolResult wrapping.
func (e *Engine) readFileContent(resolved string, args map[string]interface{}, needChecksum bool) (*readContentResult, error) {
	return e.readTextWindow(resolved, args, needChecksum)
}

// statForRead stats resolved and verifies it is a readable, regular file
// within the size cap. Errors carry suggestions for the caller to surface.
func (e *Engine) statForRead(resolved string) (os.FileInfo, error) {
	var info os.FileInfo
	var err error
	if _, relErr := e.rootRelative(resolved); relErr == nil {
		info, err = e.rootedStat(resolved)
	} else {
		info, err = os.Stat(resolved)
	}
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("file not found: %s", resolved),
				Suggestion: "verify the path exists with list_dir on the parent, or check for typos",
				Code:       ErrCodeFileNotFound,
			}
		}
		if os.IsPermission(err) {
			return nil, permissionDeniedErr(resolved)
		}
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("not a regular file: %s", resolved),
			Suggestion: "target a regular file, not a directory — use list_dir to enumerate entries",
			Code:       ErrCodeInvalidArgs,
		}
	}
	if info.Size() > maxFileSize {
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("file too large: %d MB (max 50 MB)", info.Size()>>20),
			Suggestion: "file is too large to read in one shot; use start_line/end_line to window, or search_files for a pattern",
			Code:       ErrCodeFileTooLarge,
		}
	}
	return info, nil
}
