package jinn

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (e *Engine) readFile(args map[string]interface{}) (*ToolResult, error) {
	path, _ := args["path"].(string)
	resolved, err := e.checkPathForRead(path)
	if err != nil {
		// Wrap with "blocked:" prefix for backward compat, preserving any
		// ErrWithSuggestion so callers can surface the suggestion and Code fields.
		var sErr *ErrWithSuggestion
		if errors.As(err, &sErr) {
			code := sErr.Code
			if code == "" {
				code = ErrCodePathOutsideSandbox
			}
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("blocked: %w", sErr.Err),
				Suggestion: sErr.Suggestion,
				Code:       code,
			}
		}
		return nil, &ErrWithSuggestion{
			Err:        fmt.Errorf("blocked: %w", err),
			Suggestion: "path is blocked by sandbox policy; supply a path inside the workdir",
			Code:       ErrCodePathOutsideSandbox,
		}
	}

	ext := strings.ToLower(filepath.Ext(resolved))

	// Image detection: peek at the first 512 bytes for MIME sniffing.
	// SVG returns text/xml from DetectContentType, so check extension too.
	isImage := false
	var detected string
	if data, ferr := peekFileBytes(resolved, 512); ferr == nil && len(data) > 0 {
		detected, isImage = imageMIME(data)
	}
	if !isImage && ext == ".svg" {
		isImage = true
	}

	if isImage {
		return e.readImageFile(resolved, path, detected, args)
	}

	// Conditional read: if the caller supplies if_checksum and it matches the
	// current file's SHA-256, return a compact unchanged response (no content).
	var res *ToolResult
	if res, err = readUnchangedIfMatch(resolved, path, args); err != nil || res != nil {
		return res, err
	}

	// Delegate to the shared content reader.
	result, err := e.readFileContent(resolved, args)
	if err != nil {
		return binaryFallbackOrErr(resolved, args, err)
	}

	// Compute checksum if requested (separate read; include_checksum is rare).
	checksum := checksumIfRequested(resolved, args)

	// Build final text: content + byte/window hint.
	text := result.Content
	if result.ByteHint != "" {
		text += result.ByteHint
	}

	// Wrap in ToolResult with truncation metadata when applicable.
	if result.Truncated {
		return withChecksum(&ToolResult{
			Text: text,
			Meta: map[string]any{
				"truncation": truncationInfo{
					Truncated:   true,
					TotalLines:  result.TotalLines,
					OutputLines: result.OutputLines,
				},
			},
		}, checksum), nil
	}

	return withChecksum(textResult(text), checksum), nil
}

// readImageFile reads an image file and returns it as a base64 image block,
// recording it in the tracker and attaching a checksum when requested.
func (e *Engine) readImageFile(resolved, path, detected string, args map[string]interface{}) (*ToolResult, error) {
	data, rerr := os.ReadFile(resolved)
	if rerr != nil {
		if os.IsPermission(rerr) {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("permission denied: %s", path),
				Suggestion: "file is not readable by the sandbox; check ownership or choose a different file",
				Code:       ErrCodePermissionDenied,
			}
		}
		return nil, rerr
	}

	info, serr := os.Stat(resolved)
	if serr == nil {
		e.tracker.record(resolved, info.ModTime())
	}

	var checksum string
	if inc, _ := args["include_checksum"].(bool); inc {
		h := sha256.Sum256(data)
		checksum = hex.EncodeToString(h[:])
	}

	mime := "image/svg+xml"
	if strings.HasPrefix(detected, "image/") {
		mime = detected
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	return withChecksum(&ToolResult{
		Content: []ContentBlock{{
			Type:     "image",
			Data:     encoded,
			MimeType: mime,
		}},
	}, checksum), nil
}

// readUnchangedIfMatch implements the if_checksum precondition: when the
// caller's checksum matches the file's current SHA-256, it returns a compact
// "unchanged" result. A nil result with nil error means the caller should
// proceed with a normal read.
func readUnchangedIfMatch(resolved, path string, args map[string]interface{}) (*ToolResult, error) {
	ifCS, _ := args["if_checksum"].(string)
	if ifCS == "" {
		return nil, nil
	}
	d, rerr := os.ReadFile(resolved)
	if rerr != nil {
		if os.IsPermission(rerr) {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("permission denied: %s", path),
				Suggestion: "file is not readable by the sandbox; check ownership or choose a different file",
				Code:       ErrCodePermissionDenied,
			}
		}
		return nil, rerr
	}
	h := sha256.Sum256(d)
	current := hex.EncodeToString(h[:])
	if ifCS == current {
		return &ToolResult{
			Text: fmt.Sprintf(`{"unchanged":true,"path":%q,"checksum":%q}`, resolved, current),
		}, nil
	}
	return nil, nil
}

// binaryFallbackOrErr converts the binary-file ErrWithSuggestion from
// readFileContent into a backward-compatible bracketed text result; any other
// error is returned unchanged.
func binaryFallbackOrErr(resolved string, args map[string]interface{}, err error) (*ToolResult, error) {
	var sErr *ErrWithSuggestion
	if errors.As(err, &sErr) && sErr.Code == ErrCodeBinaryFile && strings.HasPrefix(sErr.Err.Error(), "binary file:") {
		checksum := checksumIfRequested(resolved, args)
		return withChecksum(textResult(fmt.Sprintf("[%s — %s]", sErr.Err.Error(), sErr.Suggestion)), checksum), nil
	}
	return nil, err
}

// checksumIfRequested re-reads the file and returns its SHA-256 hex digest when
// include_checksum is set; otherwise the empty string. Read errors yield "".
func checksumIfRequested(resolved string, args map[string]interface{}) string {
	inc, _ := args["include_checksum"].(bool)
	if !inc {
		return ""
	}
	d, rerr := os.ReadFile(resolved)
	if rerr != nil {
		return ""
	}
	h := sha256.Sum256(d)
	return hex.EncodeToString(h[:])
}

// withChecksum adds a SHA-256 checksum to a ToolResult's Meta map.
// If checksum is empty, the result is returned unchanged.
func withChecksum(tr *ToolResult, checksum string) *ToolResult {
	if checksum == "" {
		return tr
	}
	if tr.Meta == nil {
		tr.Meta = map[string]any{}
	}
	tr.Meta["sha256"] = checksum
	return tr
}
