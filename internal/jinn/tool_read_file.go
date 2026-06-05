package jinn

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

func (e *Engine) readFile(args map[string]interface{}) (*ToolResult, error) {
	path, _ := args["path"].(string)
	resolved, err := e.checkPathForRead(path)
	if err != nil {
		return nil, wrapBlockedReadErr(err)
	}
	if _, statErr := statForRead(resolved); statErr != nil {
		return nil, statErr
	}

	// Image detection: single source of truth in detectIsImage.
	detected, isImage := detectIsImage(resolved, resolved)
	if isImage {
		return e.readImageFile(resolved, path, detected, args)
	}

	return e.readTextFile(resolved, args)
}

// wrapBlockedReadErr re-wraps a path-check error with the "blocked:" prefix for
// backward compat, preserving any ErrWithSuggestion's suggestion and Code.
func wrapBlockedReadErr(err error) error {
	var sErr *ErrWithSuggestion
	if errors.As(err, &sErr) {
		code := sErr.Code
		if code == "" {
			code = ErrCodePathOutsideSandbox
		}
		return &ErrWithSuggestion{
			Err:        fmt.Errorf("blocked: %w", sErr.Err),
			Suggestion: sErr.Suggestion,
			Code:       code,
		}
	}
	return &ErrWithSuggestion{
		Err:        fmt.Errorf("blocked: %w", err),
		Suggestion: "path is blocked by sandbox policy; supply a path inside the workdir",
		Code:       ErrCodePathOutsideSandbox,
	}
}

// readTextFile delegates to the shared content reader, applies the binary
// fallback, and assembles the final ToolResult with truncation metadata and an
// optional checksum.
func (e *Engine) readTextFile(resolved string, args map[string]interface{}) (*ToolResult, error) {
	result, err := e.readFileContent(resolved, args, shouldComputeChecksum(args))
	if err != nil {
		checksum := ""
		if result != nil {
			checksum = result.Checksum
		}
		return binaryFallbackOrErr(err, checksum)
	}

	if ifChecksum, ok := args["if_checksum"].(string); ok && ifChecksum != "" {
		if ifChecksum == result.Checksum {
			return &ToolResult{
				Text: fmt.Sprintf(`{"unchanged":true,"path":%q,"checksum":%q}`, resolved, result.Checksum),
			}, nil
		}
	}

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
		}, result.Checksum), nil
	}

	return withChecksum(textResult(text), result.Checksum), nil
}

// readImageFile reads an image file and returns it as a base64 image block,
// recording it in the tracker and attaching a checksum when requested.
func (e *Engine) readImageFile(resolved, path, detected string, args map[string]interface{}) (*ToolResult, error) {
	data, rerr := os.ReadFile(resolved)
	if rerr != nil {
		if os.IsPermission(rerr) {
			return nil, permissionDeniedErr(path)
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

// checksumRequested returns true when any checksum-relevant input requires a file
// read.
func checksumRequested(args map[string]interface{}) bool {
	if inc, ok := args["include_checksum"].(bool); ok && inc {
		return true
	}
	if ifChecksum, _ := args["if_checksum"].(string); ifChecksum != "" {
		return true
	}
	return false
}

// binaryFallbackOrErr converts the binary-file ErrWithSuggestion from
// readFileContent into a backward-compatible bracketed text result; any other
// error is returned unchanged.
func binaryFallbackOrErr(err error, checksum string) (*ToolResult, error) {
	var sErr *ErrWithSuggestion
	if errors.As(err, &sErr) && sErr.Code == ErrCodeBinaryFile && strings.HasPrefix(sErr.Err.Error(), "binary file:") {
		return withChecksum(textResult(fmt.Sprintf("[%s — %s]", sErr.Err.Error(), sErr.Suggestion)), checksum), nil
	}
	return nil, err
}

// shouldComputeChecksum is a local alias for checksumRequested to keep caller intent clear.
func shouldComputeChecksum(args map[string]interface{}) bool {
	return checksumRequested(args)
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
