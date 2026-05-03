package jinn

// Error code constants for structured error reporting.
const (
	ErrCodePathOutsideSandbox = "path_outside_sandbox"
	ErrCodeFileNotFound       = "file_not_found"
	ErrCodePermissionDenied   = "permission_denied"
	ErrCodeEditNotUnique      = "edit_not_unique"
	ErrCodeEditNoChange       = "edit_no_change"
	ErrCodeEditNotFound       = "edit_not_found"
	ErrCodeEditOverlap        = "edit_overlap"
	ErrCodeOldTextEmpty       = "old_text_empty"
	ErrCodeTimeout            = "timeout"
	ErrCodeLspUnavailable     = "lsp_unavailable"
	ErrCodeBinaryFile         = "binary_file"
	ErrCodeFileTooLarge       = "file_too_large"
	ErrCodeInvalidRegex       = "invalid_regex"
	ErrCodeStaleFile          = "stale_file"
	ErrCodeCommandBlocked     = "command_blocked"
	ErrCodeInvalidArgs        = "invalid_args"
)

// ErrWithSuggestion wraps an error with a user-facing suggestion and an
// optional machine-readable error code.
type ErrWithSuggestion struct {
	Err        error
	Suggestion string
	Code       string
}

func (e *ErrWithSuggestion) Error() string { return e.Err.Error() }
func (e *ErrWithSuggestion) Unwrap() error { return e.Err }
