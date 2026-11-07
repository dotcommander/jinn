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
// original error is returned unchanged. When some members already committed,
// the error ends with an exact batch receipt naming the partial set, the
// failed member, the publication total, and the unstarted set; committed
// edits are never replayed: the caller retries only failed and unstarted
// members.
// partialApplyInfo bundles the partial-apply reporting context:
// the operation name, the applied set, the unstarted set, the failed
// member, and the publication total.
type partialApplyInfo struct {
	op        string
	applied   []appliedRef
	unstarted []string
	failed    string
	total     int
}

func partialApplyErr(info partialApplyInfo, failErr error) error {
	op, applied, unstarted, failed, total := info.op, info.applied, info.unstarted, info.failed, info.total
	if len(applied) == 0 {
		return failErr
	}
	parts := make([]string, len(applied))
	committed := make(map[string]bool)
	for i, a := range applied {
		key := a.resolved
		if key == "" {
			key = a.path
		}
		committed[key] = true
		if a.undoID != "" {
			parts[i] = fmt.Sprintf("%s (undo id=%s)", a.path, a.undoID)
		} else {
			parts[i] = a.path
		}
	}
	remaining := append([]string{}, unstarted...)
	receipt := fmt.Sprintf(" [batch: partial=%d; failed=%s; publication=%d; unstarted=%s]",
		len(parts), failed, total, strings.Join(remaining, ", "))
	code := ErrCodeConflict
	var sErr *ErrWithSuggestion
	if errors.As(failErr, &sErr) && sErr.Code != "" {
		code = sErr.Code
	}
	return &ErrWithSuggestion{
		Err: fmt.Errorf("%s: partial apply — %d of %d files already written: %s; %w%s",
			op, len(committed), total, strings.Join(parts, ", "), failErr, receipt),
		Suggestion: `restore already-applied files with undo action="restore" id=<id>, then fix the error and retry only the failed and unstarted members`,
		Code:       code,
	}
}
