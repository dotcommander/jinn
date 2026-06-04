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
		// ErrWithSuggestion so callers can surface the suggestion field.
		var sErr *ErrWithSuggestion
		if errors.As(err, &sErr) {
			return nil, &ErrWithSuggestion{
				Err:        fmt.Errorf("blocked: %w", sErr.Err),
				Suggestion: sErr.Suggestion,
				Code:       ErrCodePathOutsideSandbox,
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

		var mime string
		if strings.HasPrefix(detected, "image/") {
			mime = detected
		} else {
			mime = "image/svg+xml"
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

	// Conditional read: if the caller supplies if_checksum and it matches the
	// current file's SHA-256, return a compact unchanged response (no content).
	if ifCS, _ := args["if_checksum"].(string); ifCS != "" {
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
	}

	// Delegate to the shared content reader.
	result, err := e.readFileContent(resolved, args)
	if err != nil {
		// Binary detection returns an ErrWithSuggestion — convert to the
		// backward-compatible textResult format with a bracketed hint.
		var sErr *ErrWithSuggestion
		if errors.As(err, &sErr) && sErr.Code == ErrCodeBinaryFile && strings.HasPrefix(sErr.Err.Error(), "binary file:") {
			var checksum string
			if inc, _ := args["include_checksum"].(bool); inc {
				if d, rerr := os.ReadFile(resolved); rerr == nil {
					h := sha256.Sum256(d)
					checksum = hex.EncodeToString(h[:])
				}
			}
			return withChecksum(textResult(fmt.Sprintf("[%s — %s]", sErr.Err.Error(), sErr.Suggestion)), checksum), nil
		}
		return nil, err
	}

	// Compute checksum if requested (separate read; include_checksum is rare).
	var checksum string
	if inc, _ := args["include_checksum"].(bool); inc {
		if d, rerr := os.ReadFile(resolved); rerr == nil {
			h := sha256.Sum256(d)
			checksum = hex.EncodeToString(h[:])
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
		}, checksum), nil
	}

	return withChecksum(textResult(text), checksum), nil
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
