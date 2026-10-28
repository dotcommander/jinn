package jinn

import (
	"errors"
	"fmt"
	"strings"
)

// appliedRef records one file already written during a batch, for
// partial-failure reporting. undoID may be "" (snapshots are best-effort).
type appliedRef struct{ path, undoID, resolved string }

// partialApplyErr wraps failErr with the list of files already applied so
// the agent can undo-restore them or retry only the remainder. failErr is
// expected to already name the failing file. With nothing applied yet the
// original error is returned unchanged.
func partialApplyErr(op string, applied []appliedRef, total int, failErr error) error {
	if len(applied) == 0 {
		return failErr
	}
	parts := make([]string, len(applied))
	targets := make(map[string]bool)
	for i, a := range applied {
		key := a.resolved
		if key == "" {
			key = a.path
		}
		targets[key] = true
		if a.undoID != "" {
			parts[i] = fmt.Sprintf("%s (undo id=%s)", a.path, a.undoID)
		} else {
			parts[i] = a.path
		}
	}
	code := ErrCodeConflict
	var sErr *ErrWithSuggestion
	if errors.As(failErr, &sErr) && sErr.Code != "" {
		code = sErr.Code
	}
	return &ErrWithSuggestion{
		Err: fmt.Errorf("%s: partial apply — %d of %d files already written: %s; %w",
			op, len(targets), total, strings.Join(parts, ", "), failErr),
		Suggestion: `restore already-applied files with undo action="restore" id=<id>, then fix the error and retry`,
		Code:       code,
	}
}
