package jinn

import "fmt"

// preparedWrite is one validated file replacement ready for the mutation
// phase. Callers retain their operation-specific formatting and metadata.
type preparedWrite struct {
	resolved   string
	path       string
	preContent []byte
	updated    string
}

// commitPreparedWrites applies prepared writes in order. The legacy input count
// is replaced by the canonical target count for partial-apply reporting when a
// caller coalesces several inputs into one file write. committed runs after each
// successful write and before the next preflight, preserving caller-owned timing.
func (e *Engine) commitPreparedWrites(op string, total int, writes []preparedWrite, committed func(index int)) error {
	targets := make(map[string]bool)
	for _, write := range writes {
		targets[write.resolved] = true
	}
	total = len(targets)
	var applied []appliedRef
	for index, write := range writes {
		if err := e.verifyPreflightState(write.resolved, write.preContent, true); err != nil {
			return partialApplyErr(op, applied, total, fmt.Errorf("%s: %w", write.path, err))
		}
		id, err := e.snapshotAndWrite(write.resolved, write.path, op, write.preContent, write.updated)
		if err != nil {
			return partialApplyErr(op, applied, total, fmt.Errorf("%s: %w", write.path, err))
		}
		applied = append(applied, appliedRef{path: write.path, undoID: id, resolved: write.resolved})
		if committed != nil {
			committed(index)
		}
	}
	return nil
}
